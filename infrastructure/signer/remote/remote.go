// Package remote implements the external signing-service adapter (spec
// §12.4): a signer capability whose key material lives in another
// process, reached over the HTTP JSON protocol this package defines.
//
// Protocol v1:
//
//	GET  /v1/describe → {"name","signature_types","formats",
//	                     "address_sources","accounts"}
//	POST /v1/sign     → {"network","account","type","payload"}
//	                   ← {"signature","signer"}
//
// The payload and the signature travel base64-encoded: both are exact
// bytes the client never interprets. Every request carries
// "Authorization: Bearer <token>" when a token is configured; the token is
// never rendered in errors or logs (spec §12.3).
//
// New pins the descriptor and the allow-list at construction — the same
// startup-verification pattern used for chain identity — so the caller
// learns what the service may sign before the first request. The pinned
// allow-list is enforced locally on every Sign call: a request outside it
// never leaves the process, and neither does a scheme the descriptor does
// not list.
//
// The package is stdlib-only (ADR-0001): net/http over JSON, no chain SDKs
// and no chain crypto. Signatures arrive as opaque bytes.
package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/cuihairu/chame/capability/signer"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// Defaults applied to zero config fields.
const (
	defaultTimeout = 5 * time.Second
	maxBodyBytes   = 1 << 20
)

// Config tunes the client. Zero fields take the defaults.
type Config struct {
	// URL is the signing service base URL, scheme and host included.
	URL string
	// Token authenticates every call as a bearer token. Optional.
	Token signer.Secret
	// Timeout bounds each HTTP call. Default 5s.
	Timeout time.Duration
	// Client replaces the HTTP client wholesale. When set, Timeout is
	// the caller's concern.
	Client *http.Client
}

// Client is the remote signing service as a signer capability (spec
// §12.1). It is safe for concurrent use.
type Client struct {
	url   string
	token signer.Secret
	http  *http.Client

	desc  signer.Descriptor
	allow signer.AllowList
}

var _ signer.Signer = (*Client)(nil)

// describeResponse is the v1 self-description of the service.
type describeResponse struct {
	Name           string                 `json:"name"`
	SignatureTypes []signer.SignatureType `json:"signature_types"`
	Formats        []signer.Format        `json:"formats"`
	AddressSources []signer.AddressSource `json:"address_sources"`
	Accounts       []accountWire          `json:"accounts"`
}

// accountWire is one (network, account) pair the service may sign for.
type accountWire struct {
	Network identifiers.NetworkID `json:"network"`
	Address identifiers.Address   `json:"address"`
}

// signRequest is one signing call. Payload is the exact bytes to sign,
// base64-encoded.
type signRequest struct {
	Network identifiers.NetworkID `json:"network"`
	Account identifiers.Address   `json:"address"`
	Type    string                `json:"type"`
	Payload string                `json:"payload"`
}

// signResponse is the service's answer. Signature is the scheme-defined
// signature bytes, base64-encoded.
type signResponse struct {
	Signature string `json:"signature"`
	Signer    string `json:"signer"`
}

// errorBody is the error shape protocol v1 services answer with.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// New dials the service, pins its descriptor and allow-list (spec §12.3:
// 签名器应能限制可用网络和账户), and returns the client. An unreachable
// service is a network error; a self-description the signer contract
// rejects — empty name, malformed entries, family-mismatched or duplicated
// accounts — is a configuration error the operator must fix before the
// process runs.
func New(ctx context.Context, cfg Config) (*Client, error) {
	const op = "signer.remote.describe"
	if err := validEndpoint(cfg.URL); err != nil {
		return nil, err
	}
	httpc := cfg.Client
	if httpc == nil {
		httpc = &http.Client{Timeout: withDefault(cfg.Timeout, defaultTimeout)}
	}
	c := &Client{
		url:   strings.TrimSuffix(cfg.URL, "/"),
		token: cfg.Token,
		http:  httpc,
	}

	var desc describeResponse
	if err := c.call(ctx, http.MethodGet, "/v1/describe", nil, &desc, op); err != nil {
		return nil, err
	}
	pinned := signer.Descriptor{
		Name:           desc.Name,
		SignatureTypes: desc.SignatureTypes,
		Formats:        desc.Formats,
		AddressSources: desc.AddressSources,
	}
	if err := pinned.Validate(); err != nil {
		return nil, cherrs.Wrap(err, cherrs.InvalidConfiguration, op)
	}
	allow := make(signer.AllowList, 0, len(desc.Accounts))
	for i, acc := range desc.Accounts {
		ref, err := signer.NewAccountRef(acc.Network, acc.Address)
		if err != nil {
			return nil, cherrs.New(cherrs.InvalidConfiguration, op,
				"described account %d: %v", i, err)
		}
		allow = append(allow, ref)
	}
	if err := allow.Validate(); err != nil {
		return nil, cherrs.Wrap(err, cherrs.InvalidConfiguration, op)
	}
	c.desc = pinned
	c.allow = allow
	return c, nil
}

// Descriptor returns the description pinned at construction.
func (c *Client) Descriptor() signer.Descriptor { return c.desc }

// Accounts returns the (network, account) pairs pinned at construction.
func (c *Client) Accounts() []signer.AccountRef { return c.allow.Accounts() }

// Sign asks the service for one signature. The request is validated and
// checked against the pinned descriptor and allow-list before any dial:
// nothing unsupported or unauthorized leaves the process (spec §12.3: 不得
// 仅凭调用方提供的任意数据就被认为授权成功).
func (c *Client) Sign(ctx context.Context, req signer.Request) (signer.Signed, error) {
	const op = "signer.remote.sign"
	if err := ctx.Err(); err != nil {
		return signer.Signed{}, err
	}
	if err := req.Validate(); err != nil {
		return signer.Signed{}, err
	}
	if !c.desc.SupportsType(req.Type) {
		return signer.Signed{}, fmt.Errorf("%w: %q", signer.ErrUnsupportedType, req.Type)
	}
	if err := c.allow.Authorize(req); err != nil {
		return signer.Signed{}, err
	}

	wire := signRequest{
		Network: req.Network,
		Account: req.Account,
		Type:    string(req.Type),
		Payload: base64.StdEncoding.EncodeToString(req.Payload),
	}
	var answer signResponse
	if err := c.call(ctx, http.MethodPost, "/v1/sign", wire, &answer, op); err != nil {
		return signer.Signed{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(answer.Signature)
	if err != nil {
		return signer.Signed{}, cherrs.Wrap(err, cherrs.InternalError, op)
	}
	if answer.Signer != c.desc.Name {
		return signer.Signed{}, cherrs.New(cherrs.InternalError, op,
			"answer names signer %q, want the pinned %q", answer.Signer, c.desc.Name)
	}
	out := signer.Signed{Signature: sig, Signer: answer.Signer}
	if err := out.Validate(); err != nil {
		return signer.Signed{}, cherrs.Wrap(err, cherrs.InternalError, op)
	}
	return out, nil
}

// call performs one protocol request: bearer auth, JSON in, JSON out. A
// non-200 status is mapped through statusError; a transport failure
// through classifyCall.
func (c *Client) call(ctx context.Context, method, path string, in, out any, op string) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return cherrs.Wrap(err, cherrs.InvalidArgument, op)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return cherrs.Wrap(err, cherrs.InvalidConfiguration, op)
	}
	if !c.token.IsZero() {
		req.Header.Set("Authorization", "Bearer "+string(c.token.Bytes()))
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return classifyCall(op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return classifyCall(op, err)
	}
	if resp.StatusCode != http.StatusOK {
		var eb errorBody
		_ = json.Unmarshal(raw, &eb) // best effort: the status already spoke
		return statusError(op, resp.StatusCode, eb)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return cherrs.Wrap(err, cherrs.InvalidConfiguration, op)
	}
	return nil
}

// classifyCall turns a transport failure into the shared taxonomy. Caller
// cancellation passes through unchanged; timeouts classify as
// RequestTimeout; everything else is NetworkUnavailable. The cause is
// stripped of endpoint URLs before wrapping (spec §14.2).
func classifyCall(op string, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	cause := err
	var ue *neturl.Error
	if errors.As(err, &ue) {
		cause = ue.Err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return cherrs.Wrap(cause, cherrs.RequestTimeout, op)
	}
	return cherrs.Wrap(cause, cherrs.NetworkUnavailable, op)
}

// statusError maps a service status onto the signer contract's sentinels
// and the shared taxonomy. Service-side rejections keep the signer
// semantics: 400 is a rejected request, 401/403 an authorization failure.
func statusError(op string, status int, body errorBody) error {
	switch status {
	case http.StatusBadRequest:
		return cherrs.Wrap(fmt.Errorf("%w: service rejected the request: %s",
			signer.ErrInvalidRequest, body.Message), cherrs.InvalidArgument, op)
	case http.StatusUnauthorized:
		return cherrs.Wrap(fmt.Errorf("%w: token rejected",
			signer.ErrUnauthorized), cherrs.AuthenticationFailed, op)
	case http.StatusForbidden:
		return cherrs.Wrap(fmt.Errorf("%w: %s",
			signer.ErrUnauthorized, body.Message), cherrs.PermissionDenied, op)
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return cherrs.New(cherrs.InvalidConfiguration, op,
			"service does not speak protocol v1 (status %d)", status)
	case http.StatusTooManyRequests:
		return cherrs.New(cherrs.RateLimited, op,
			"service throttled signing: %s", body.Message)
	default:
		if status >= 500 {
			return cherrs.New(cherrs.NetworkUnavailable, op,
				"service failed (status %d): %s", status, body.Message)
		}
		return cherrs.New(cherrs.InternalError, op,
			"unexpected status %d: %s", status, body.Message)
	}
}

// validEndpoint rejects URLs the HTTP client cannot dial.
func validEndpoint(raw string) error {
	if raw == "" {
		return cherrs.New(cherrs.InvalidConfiguration, "signer.remote.describe",
			"service url must not be empty")
	}
	u, err := neturl.ParseRequestURI(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return cherrs.New(cherrs.InvalidConfiguration, "signer.remote.describe",
			"service url is not a valid http(s) endpoint")
	}
	return nil
}

// withDefault substitutes def for a zero duration.
func withDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}
