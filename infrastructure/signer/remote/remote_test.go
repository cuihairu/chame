package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/signer"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

var (
	mainnet      = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}
	base         = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "base", ChainID: "8453"}
	holder       = identifiers.Address{Family: identifiers.FamilyEVM, Value: "0x00000000000000000000000000000000000000aa"}
	outsider     = identifiers.Address{Family: identifiers.FamilyEVM, Value: "0x00000000000000000000000000000000000000bb"}
	solanaHolder = identifiers.Address{
		Family: identifiers.FamilySolana,
		Value:  "4Nd1mBQtrMJVYVfKf2PJy9NZUZdTAsp7D4xWLs4gDB4T",
	}

	// signatureB64 is the canned signature the service answers with.
	signatureB64 = base64.StdEncoding.EncodeToString([]byte("signature"))
)

// service is the in-process signing-service double: httptest exercises
// the real HTTP client, so headers, bodies, and statuses are what the
// tests assert on, with no external service anywhere (spec §16.3).
type service struct {
	mu sync.Mutex

	describe   describeResponse
	signReply  signResponse
	signStatus int // 0 means 200
	delay      time.Duration

	auths    []string
	calls    []string
	requests []signRequest
}

func newService() *service {
	return &service{
		describe: describeResponse{
			Name:           "vault",
			SignatureTypes: []signer.SignatureType{signer.SigEVMMessage},
			AddressSources: []signer.AddressSource{signer.AddressSourceExternal},
			Accounts: []accountWire{
				{Network: mainnet, Address: holder},
				{Network: base, Address: holder},
			},
		},
		signReply: signResponse{Signature: signatureB64, Signer: "vault"},
	}
}

func (s *service) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auths = append(s.auths, r.Header.Get("Authorization"))
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/describe":
			_ = json.NewEncoder(w).Encode(s.describe)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sign":
			raw, _ := io.ReadAll(r.Body)
			var req signRequest
			_ = json.Unmarshal(raw, &req)
			s.mu.Lock()
			s.requests = append(s.requests, req)
			status := s.signStatus
			delay := s.delay
			s.mu.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			if status != 0 && status != http.StatusOK {
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(errorBody{Code: "denied", Message: "no"})
				return
			}
			_ = json.NewEncoder(w).Encode(s.signReply)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (s *service) snapshot() (calls []string, requests []signRequest, auths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...),
		append([]signRequest(nil), s.requests...),
		append([]string(nil), s.auths...)
}

// newClient dials the double and pins its descriptor.
func newClient(t *testing.T, svc *service, cfg Config) *Client {
	t.Helper()
	srv := httptest.NewServer(svc.handler())
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Config{URL: srv.URL, Token: signer.NewSecret([]byte("token")), Timeout: cfg.Timeout})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func signReq() signer.Request {
	return signer.Request{
		Network: mainnet,
		Account: holder,
		Type:    signer.SigEVMMessage,
		Payload: []byte("hello"),
	}
}

func TestNewPinsDescriptorAndAccounts(t *testing.T) {
	svc := newService()
	c := newClient(t, svc, Config{})

	desc := c.Descriptor()
	if desc.Name != "vault" || !desc.SupportsType(signer.SigEVMMessage) || desc.SupportsType(signer.SigSolanaMessage) {
		t.Errorf("descriptor = %+v, want the described vault", desc)
	}
	accounts := c.Accounts()
	if len(accounts) != 2 || !accounts[0].Network.Equal(mainnet) || !accounts[0].Address.Equal(holder) {
		t.Errorf("accounts = %+v, want the two described pairs", accounts)
	}
	_, requests, auths := svc.snapshot()
	if len(requests) != 0 {
		t.Errorf("describe must not sign: %+v", requests)
	}
	if len(auths) != 1 || auths[0] != "Bearer token" {
		t.Errorf("describe auth = %v, want the bearer token", auths)
	}
}

func TestNewRejectsUnreachableService(t *testing.T) {
	srv := httptest.NewServer(newService().handler())
	srv.Close() // the port is gone before anyone dials

	_, err := New(context.Background(), Config{URL: srv.URL})
	var ce *cherrs.Error
	if !errors.As(err, &ce) || ce.Category != cherrs.NetworkUnavailable {
		t.Errorf("err = %v, want network unavailable", err)
	}
}

func TestNewRejectsBadDescription(t *testing.T) {
	steps := []struct {
		name   string
		mutate func(*service)
	}{
		{"no name", func(s *service) { s.describe.Name = "" }},
		{"malformed signature type", func(s *service) {
			s.describe.SignatureTypes = []signer.SignatureType{"Bad Type"}
		}},
		{"family mismatch", func(s *service) {
			s.describe.Accounts = []accountWire{{Network: mainnet, Address: solanaHolder}}
		}},
		{"duplicate account", func(s *service) {
			s.describe.Accounts = []accountWire{
				{Network: mainnet, Address: holder},
				{Network: mainnet, Address: holder},
			}
		}},
	}
	for _, step := range steps {
		svc := newService()
		step.mutate(svc)
		srv := httptest.NewServer(svc.handler())
		if _, err := New(context.Background(), Config{URL: srv.URL}); err == nil {
			t.Errorf("%s: err = nil, want a configuration error", step.name)
		}
		srv.Close()
	}
}

func TestNewRejectsMissingURL(t *testing.T) {
	if _, err := New(context.Background(), Config{URL: ""}); err == nil {
		t.Error("empty url: err = nil, want a configuration error")
	}
}

func TestSignRoundTrip(t *testing.T) {
	svc := newService()
	c := newClient(t, svc, Config{})

	got, err := c.Sign(context.Background(), signReq())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if string(got.Signature) != "signature" || got.Signer != "vault" {
		t.Errorf("signed = %q %q, want signature/vault", got.Signature, got.Signer)
	}
	_, requests, auths := svc.snapshot()
	if len(requests) != 1 {
		t.Fatalf("sign calls = %d, want 1", len(requests))
	}
	req := requests[0]
	if !req.Network.Equal(mainnet) || !req.Account.Equal(holder) || req.Type != "evm-message" {
		t.Errorf("wire request = %+v, want mainnet/holder/evm-message", req)
	}
	wantPayload := base64.StdEncoding.EncodeToString([]byte("hello"))
	if req.Payload != wantPayload {
		t.Errorf("payload = %q, want %q", req.Payload, wantPayload)
	}
	if len(auths) != 2 || auths[1] != "Bearer token" {
		t.Errorf("sign auth = %v, want the bearer token on both calls", auths)
	}
}

func TestSignRejectsBeforeDialing(t *testing.T) {
	svc := newService()
	c := newClient(t, svc, Config{})

	// A scheme the descriptor does not list.
	req := signReq()
	req.Type = signer.SigSolanaMessage
	if _, err := c.Sign(context.Background(), req); !errors.Is(err, signer.ErrUnsupportedType) {
		t.Errorf("unsupported type: err = %v, want unsupported type", err)
	}

	// An account the allow-list does not cover.
	req = signReq()
	req.Account = outsider
	if _, err := c.Sign(context.Background(), req); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("outside allow-list: err = %v, want unauthorized", err)
	}

	// A malformed request: family mismatch.
	req = signReq()
	req.Network = identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet", ChainID: "1"}
	if _, err := c.Sign(context.Background(), req); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("family mismatch: err = %v, want invalid request", err)
	}

	// None of the rejections may have reached the service.
	calls, _, _ := svc.snapshot()
	for _, call := range calls {
		if call == "POST /v1/sign" {
			t.Errorf("a rejected request must not dial the service, got %v", calls)
		}
	}
}

func TestSignMapsServiceStatuses(t *testing.T) {
	steps := []struct {
		status int
		check  func(t *testing.T, err error)
	}{
		{http.StatusBadRequest, func(t *testing.T, err error) {
			if !errors.Is(err, signer.ErrInvalidRequest) {
				t.Errorf("status 400: err = %v, want invalid request", err)
			}
		}},
		{http.StatusUnauthorized, func(t *testing.T, err error) {
			if !errors.Is(err, signer.ErrUnauthorized) {
				t.Errorf("status 401: err = %v, want unauthorized", err)
			}
		}},
		{http.StatusForbidden, func(t *testing.T, err error) {
			if !errors.Is(err, signer.ErrUnauthorized) {
				t.Errorf("status 403: err = %v, want unauthorized", err)
			}
		}},
		{http.StatusTooManyRequests, func(t *testing.T, err error) {
			cat, ok := cherrs.CategoryOf(err)
			if !ok || cat != cherrs.RateLimited {
				t.Errorf("status 429: err = %v, want rate limited", err)
			}
		}},
		{http.StatusInternalServerError, func(t *testing.T, err error) {
			cat, ok := cherrs.CategoryOf(err)
			if !ok || cat != cherrs.NetworkUnavailable {
				t.Errorf("status 500: err = %v, want network unavailable", err)
			}
		}},
	}
	for _, step := range steps {
		svc := newService()
		svc.signStatus = step.status
		c := newClient(t, svc, Config{})
		_, err := c.Sign(context.Background(), signReq())
		if err == nil {
			t.Fatalf("status %d: err = nil, want error", step.status)
		}
		step.check(t, err)
	}
}

func TestSignTimesOut(t *testing.T) {
	svc := newService()
	svc.delay = 200 * time.Millisecond
	c := newClient(t, svc, Config{Timeout: 30 * time.Millisecond})

	_, err := c.Sign(context.Background(), signReq())
	cat, ok := cherrs.CategoryOf(err)
	if !ok || cat != cherrs.RequestTimeout {
		t.Errorf("err = %v, want request timeout", err)
	}
}

func TestSignRejectsBadAnswers(t *testing.T) {
	svc := newService()
	svc.signReply.Signature = ""
	c := newClient(t, svc, Config{})
	if _, err := c.Sign(context.Background(), signReq()); err == nil {
		t.Error("empty signature: err = nil, want error")
	}

	svc2 := newService()
	svc2.signReply.Signer = "someone-else"
	c2 := newClient(t, svc2, Config{})
	if _, err := c2.Sign(context.Background(), signReq()); err == nil {
		t.Error("mismatched signer name: err = nil, want error")
	}
}

func TestSignHonorsCanceledContext(t *testing.T) {
	svc := newService()
	c := newClient(t, svc, Config{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Sign(ctx, signReq()); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context canceled", err)
	}
	calls, _, _ := svc.snapshot()
	if len(calls) != 1 { // only the describe from construction
		t.Errorf("calls = %v, want no dial after cancellation", calls)
	}
}
