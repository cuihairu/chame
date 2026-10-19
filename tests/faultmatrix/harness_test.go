// Package faultmatrix exercises the JSON-RPC transport fault boundary used
// by adapter RPC clients. It is a deterministic local harness, not a chain node.
package faultmatrix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fault string

const (
	none        fault = "none"
	timeout     fault = "timeout"
	rateLimit   fault = "429"
	serverError fault = "5xx"
	interrupt   fault = "disconnect"
	slow        fault = "slow"
)

func rpcCall(ctx context.Context, url, method string) (retErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, resp.Body.Close()) }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("read RPC response: %w", err)
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected http %d", resp.StatusCode)
	}
	return nil
}

func endpoint(f fault) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch f {
		case timeout:
			time.Sleep(60 * time.Millisecond)
		case slow:
			time.Sleep(20 * time.Millisecond)
		case rateLimit:
			w.WriteHeader(429)
			return
		case serverError:
			w.WriteHeader(503)
			return
		case interrupt:
			hj, ok := w.(http.Hijacker)
			if ok {
				c, _, _ := hj.Hijack()
				_ = c.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
	}))
}

// TestFixedScenariosAcrossFaults runs each requested operation against each
// injected transport condition. The server returns a fixed synthetic balance;
// transaction submission and event polling are represented by their RPC method
// class only, not by a signed transaction or chain state transition.
func TestFixedScenariosAcrossFaults(t *testing.T) {
	scenarios := []struct{ name, method string }{
		{"balance read", "eth_getBalance"}, {"fake-signer transaction RPC", "eth_sendRawTransaction"}, {"event poll RPC", "eth_getLogs"},
		{"Solana balance read", "getBalance"}, {"Solana fake-signer transaction RPC", "sendTransaction"}, {"Solana event poll RPC", "getSignaturesForAddress"},
	}
	faults := []fault{none, timeout, rateLimit, serverError, interrupt, slow}
	for _, scenario := range scenarios {
		for _, f := range faults {
			t.Run(scenario.name+"/"+string(f), func(t *testing.T) {
				s := endpoint(f)
				defer s.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				err := rpcCall(ctx, s.URL, scenario.method)
				if f == none || f == slow {
					if err != nil {
						t.Fatalf("expected success: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("expected transport failure")
				}
			})
		}
	}
}

// TestRestartRecovery resumes the same fixed read after endpoint interruption.
func TestRestartRecovery(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)
	}))
	defer s.Close()
	if err := rpcCall(context.Background(), s.URL, "eth_getBalance"); err == nil {
		t.Fatal("first request should fail")
	}
	if err := rpcCall(context.Background(), s.URL, "eth_getBalance"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	t.Logf("recovery calls=%d; resumed synthetic read after injected 503", calls.Load())
}

// BenchmarkConcurrentQueries provides a local HTTP transport baseline only.
func BenchmarkConcurrentQueries(b *testing.B) {
	s := endpoint(none)
	defer s.Close()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := rpcCall(context.Background(), s.URL, "eth_getBalance"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
