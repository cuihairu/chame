package devchain

import (
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// testKey signs the raw transactions the tests submit.
func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return key
}

func newTestChain(t *testing.T) (*Chain, *Node, *httptest.Server) {
	t.Helper()
	c := NewChain(1337, 20*time.Millisecond)
	t.Cleanup(c.Stop)
	n := c.NewNode(Faults{})
	srv := httptest.NewServer(n)
	t.Cleanup(srv.Close)
	return c, n, srv
}

func rpcCall(t *testing.T, url, method string, params any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", method, resp.StatusCode)
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s: decode: %v", method, err)
	}
	if out.Error != nil {
		t.Fatalf("%s: rpc error: %s", method, out.Error.Message)
	}
	return out.Result
}

// signedTx builds and signs one legacy transfer.
func signedTx(t *testing.T, key *ecdsa.PrivateKey, nonce uint64) *types.Transaction {
	t.Helper()
	signer := types.LatestSignerForChainID(big.NewInt(1337))
	tx := types.NewTransaction(nonce, common.HexToAddress("0x1111111111111111111111111111111111111111"), big.NewInt(1), 21000, big.NewInt(1_000_000_000), nil)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func submit(t *testing.T, url string, tx *types.Transaction) string {
	t.Helper()
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var hash string
	if err := json.Unmarshal(rpcCall(t, url, "eth_sendRawTransaction", []any{"0x" + common.Bytes2Hex(raw)}), &hash); err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hash
}

func TestChainMinesAndServesReceipts(t *testing.T) {
	_, _, url := newTestChain(t)
	key := testKey(t)

	var chainID string
	if err := json.Unmarshal(rpcCall(t, url.URL, "eth_chainId", []any{}), &chainID); err != nil {
		t.Fatalf("chainId: %v", err)
	}
	if chainID != "0x539" {
		t.Errorf("chain id = %s, want 0x539", chainID)
	}

	hash := submit(t, url.URL, signedTx(t, key, 0))
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp := rpcCall(t, url.URL, "eth_getTransactionReceipt", []any{hash})
		var receipt map[string]any
		if err := json.Unmarshal(resp, &receipt); err != nil {
			t.Fatalf("receipt decode: %v", err)
		}
		if receipt != nil {
			if receipt["status"] != "0x1" {
				t.Errorf("status = %v, want 0x1", receipt["status"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transaction never mined")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFaultsErrorRateAndDown(t *testing.T) {
	_, node, url := newTestChain(t)

	if err := node.SetFaults(Faults{ErrorRate: 1.0}); err != nil {
		t.Fatalf("set faults: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_chainId", "params": []any{}})
	resp, err := http.Post(url.URL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	var out struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	_ = resp.Body.Close()
	if out.Error == nil || !strings.Contains(out.Error.Message, "injected") {
		t.Errorf("error rate 1.0 did not inject a failure: %+v", out)
	}

	if err := node.SetFaults(Faults{Down: true}); err != nil {
		t.Fatalf("set faults: %v", err)
	}
	resp, err = http.Post(url.URL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("down endpoint answered %d, want 503", resp.StatusCode)
	}

	if err := node.SetFaults(Faults{ErrorRate: 2.0}); err == nil {
		t.Error("out-of-range error rate accepted")
	}
}

func TestDropRateKeepsTransactionsUnmined(t *testing.T) {
	c, node, url := newTestChain(t)
	key := testKey(t)
	if err := node.SetFaults(Faults{DropRate: 1.0}); err != nil {
		t.Fatalf("set faults: %v", err)
	}
	hash := submit(t, url.URL, signedTx(t, key, 0))
	// A dropped transaction is "accepted" and then forgotten: several
	// block intervals later there is still no receipt, and it never
	// enters the pending pool.
	time.Sleep(10 * c.blockTime)
	resp := rpcCall(t, url.URL, "eth_getTransactionReceipt", []any{hash})
	var receipt map[string]any
	if err := json.Unmarshal(resp, &receipt); err != nil {
		t.Fatalf("receipt decode: %v", err)
	}
	if receipt != nil {
		t.Fatal("dropped transaction was mined")
	}
	if c.PendingCount() != 0 {
		t.Errorf("dropped transaction held in pending pool")
	}
}

func TestFaultValidation(t *testing.T) {
	var f Faults
	if err := f.Validate(); err != nil {
		t.Errorf("zero faults rejected: %v", err)
	}
	f = Faults{ErrorRate: -0.1}
	if err := f.Validate(); err == nil {
		t.Error("negative error rate accepted")
	}
}
