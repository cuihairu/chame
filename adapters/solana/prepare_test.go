package solana

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// mustTransfer builds the System Program native transfer: program
// "11111111111111111111111111111111", payer and recipient accounts, and
// the 2/8-byte-little-endian discriminator. It is the canonical smallest
// state-changing Solana instruction, so tests can read an assembled
// message back and check who signs what.
func mustTransfer(t *testing.T, payer, to identifiers.Address, lamports uint64) Instruction {
	t.Helper()
	amount := make([]byte, 8)
	binary.LittleEndian.PutUint64(amount, lamports)
	return Instruction{
		Program: mustAddress(t, sysAccount),
		Data:    append([]byte{2}, amount...),
		Accounts: []AccountRef{
			{Address: payer, Writable: true, Signer: true},
			{Address: to, Writable: true},
		},
	}
}

func buildQuery(t *testing.T, payer identifiers.Address) BuildQuery {
	t.Helper()
	return BuildQuery{
		FeePayer: payer,
		Instructions: []Instruction{
			mustTransfer(t, payer, mustAddress(t, tokenAccount), 5_000),
		},
		Commitment:     CommitmentConfirmed,
		Deadline:       5 * time.Second,
		IdempotencyKey: "op-1",
	}
}

// devSignerFor wires the development key to exactly this network.
func devSignerFor(t *testing.T, n *Network) *LocalDevSigner {
	t.Helper()
	ref, err := signer.NewAccountRef(n.ID(), devAddress(t))
	if err != nil {
		t.Fatalf("account ref: %v", err)
	}
	s, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), ref)
	if err != nil {
		t.Fatalf("dev signer: %v", err)
	}
	return s
}

// buildFixture builds one prepared transaction over the fake.
func buildFixture(t *testing.T, f *fakeClient) (*Network, Built) {
	t.Helper()
	f.withBlockhash(solanaHashFor(0x20), 900)
	n := newTestNetwork(f)
	b, err := n.Build(context.Background(), buildQuery(t, devAddress(t)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return n, b
}

// signedFixture builds and signs one transaction over the fake.
func signedFixture(t *testing.T, f *fakeClient) (*Network, []byte) {
	t.Helper()
	n, b := buildFixture(t, f)
	signed, err := n.Sign(context.Background(), b, devSignerFor(t, n))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return n, signed
}

// TestBuildPinsBlockhashAndSigners reads a fresh blockhash and names the
// signature order the message compiled to.
func TestBuildPinsBlockhashAndSigners(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	_, b := buildFixture(t, f)

	if b.Pin.Blockhash != solanaHashFor(0x20).String() {
		t.Errorf("blockhash = %q, want the node's pinned hash", b.Pin.Blockhash)
	}
	if b.Pin.LastValidBlockHeight != 900 {
		t.Errorf("last valid height = %d, want 900", b.Pin.LastValidBlockHeight)
	}
	if b.FeePayer != devAddress(t) {
		t.Errorf("fee payer = %s, want the query's payer", b.FeePayer)
	}
	if b.Format != signer.FormatSolanaV0 {
		t.Errorf("format = %s, want %s", b.Format, signer.FormatSolanaV0)
	}
	if b.IdempotencyKey != "op-1" {
		t.Errorf("idempotency key = %q, want it carried through", b.IdempotencyKey)
	}
	if b.Deadline != 5*time.Second {
		t.Errorf("deadline = %s, want it carried through", b.Deadline)
	}
	if b.Commitment != CommitmentConfirmed {
		t.Errorf("commitment = %s, want it carried through", b.Commitment)
	}
	if len(b.Payload) == 0 {
		t.Error("payload = empty, want the unsigned message bytes")
	}
	if len(b.Signers) != 1 || b.Signers[0] != devAddress(t) {
		t.Errorf("signers = %v, want the fee payer first", b.Signers)
	}
	commitments := f.capturedCommitments()
	if len(commitments) != 1 || commitments[0] != CommitmentConfirmed {
		t.Errorf("commitments = %v, want one blockhash read at the query's commitment", commitments)
	}
	if f.callCount("latest_blockhash") != 1 {
		t.Errorf("latest blockhash calls = %d, want exactly one pin per build", f.callCount("latest_blockhash"))
	}
}

// TestBuildRecordsSignerOrder compiles a message with two signers and
// checks the signature order matches the message's account keys.
func TestBuildRecordsSignerOrder(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockhash(solanaHashFor(0x20), 900)
	n := newTestNetwork(f)
	second := mustAddress(t, voteAccount)
	q := buildQuery(t, devAddress(t))
	q.Instructions = []Instruction{{
		Program: mustAddress(t, sysAccount),
		Data:    []byte{0},
		Accounts: []AccountRef{
			{Address: devAddress(t), Writable: true, Signer: true},
			{Address: second, Writable: true, Signer: true},
		},
	}}

	b, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(b.Signers) != 2 || b.Signers[0] != devAddress(t) || b.Signers[1] != second {
		t.Errorf("signers = %v, want fee payer then the co-signer", b.Signers)
	}
}

// TestBuildRejectsInvalidQueries refuses malformed intents before any
// chain call: a build is cheap to reject and must not pin a blockhash
// for a query that cannot compile.
func TestBuildRejectsInvalidQueries(t *testing.T) {
	payer := devAddress(t)
	cases := []struct {
		name string
		q    BuildQuery
	}{
		{"no instructions", BuildQuery{FeePayer: payer}},
		{"no fee payer", func() BuildQuery {
			q := buildQuery(t, payer)
			q.FeePayer = identifiers.Address{}
			return q
		}()},
		{"evm fee payer", func() BuildQuery {
			q := buildQuery(t, payer)
			q.FeePayer, _ = identifiers.NewEVMAddress("0x00000000000000000000000000000000000000cc")
			return q
		}()},
		{"evm program", func() BuildQuery {
			q := buildQuery(t, payer)
			q.Instructions[0].Program, _ = identifiers.NewEVMAddress("0x00000000000000000000000000000000000000cc")
			return q
		}()},
		{"empty program", func() BuildQuery {
			q := buildQuery(t, payer)
			q.Instructions[0].Program = identifiers.Address{}
			return q
		}()},
		{"bad account", func() BuildQuery {
			q := buildQuery(t, payer)
			q.Instructions[0].Accounts[1].Address.Value = "not base58"
			return q
		}()},
		{"negative deadline", func() BuildQuery {
			q := buildQuery(t, payer)
			q.Deadline = -time.Second
			return q
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeClient(solanaHashFor(0x10))
			n := newTestNetwork(f)
			if _, err := n.Build(context.Background(), tc.q); err == nil {
				t.Fatal("Build: err = nil, want error")
			} else if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.InvalidArgument {
				t.Errorf("err = %v, want invalid argument", err)
			}
			if f.callCount("latest_blockhash") != 0 {
				t.Error("a rejected query must not pin a blockhash")
			}
		})
	}
}

// TestBuildDeadlineBoundsTheBuild bounds the whole build: a node that
// never answers the blockhash fails the build as a request timeout
// rather than hanging.
func TestBuildDeadlineBoundsTheBuild(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withSlow("latest_blockhash", 200*time.Millisecond)
	n := newTestNetwork(f)
	q := buildQuery(t, devAddress(t))
	q.Deadline = 20 * time.Millisecond

	_, err := n.Build(context.Background(), q)
	if err == nil {
		t.Fatal("Build over a hung node: err = nil, want error")
	}
	if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.RequestTimeout {
		t.Errorf("err = %v, want request timeout", err)
	}
}

// TestSignAssemblesSignedTransaction signs the compiled message once per
// required signer and produces a transaction the chain would accept:
// every signature verifies against the message it was made over.
func TestSignAssemblesSignedTransaction(t *testing.T) {
	_, signed := signedFixture(t, newFakeClient(solanaHashFor(0x10)))
	if len(signed) == 0 {
		t.Fatal("signed payload = empty, want a serialised transaction")
	}
	var tx solana.Transaction
	if err := tx.UnmarshalBase64(base64.StdEncoding.EncodeToString(signed)); err != nil {
		t.Fatalf("decode signed transaction: %v", err)
	}
	if err := tx.VerifySignatures(); err != nil {
		t.Errorf("signatures do not verify against the message: %v", err)
	}
}

// TestSignRefusesAMissingSigner refuses to produce a transaction whose
// required signature is not covered: a hole in the signature list is not
// a submittable transaction, and guessing is worse than stopping.
func TestSignRefusesAMissingSigner(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n, b := buildFixture(t, f)
	// A signer for a different network covers nothing here.
	other, err := signer.NewAccountRef(
		identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "other", ChainID: testGenesis(0x77)},
		devAddress(t),
	)
	if err != nil {
		t.Fatalf("account ref: %v", err)
	}
	s, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), other)
	if err != nil {
		t.Fatalf("dev signer: %v", err)
	}

	if _, err := n.Sign(context.Background(), b, s); err == nil {
		t.Fatal("Sign with an uncovered signer: err = nil, want error")
	}
	if f.callCount("send_transaction") != 0 {
		t.Error("an unsigned transaction must not reach the chain")
	}
}

// TestSubmitBroadcastsAndReturnsSignature submits the signed bytes and
// reports the signature the chain names.
func TestSubmitBroadcastsAndReturnsSignature(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withSentSignature(sigFor(0x33))
	n, signed := signedFixture(t, f)

	sig, err := n.Submit(context.Background(), signed)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if want := testSignature(0x33); sig != want {
		t.Errorf("signature = %s, want the chain's answer %s", sig, want)
	}
	sent := f.capturedSentTransaction()
	if len(sent) == 0 {
		t.Fatal("the node saw no transaction")
	}
	if string(sent) != string(signed) {
		t.Error("the submitted bytes are not the signed payload")
	}
}

// TestSubmitRejectsMalformedPayload refuses bytes that are not a
// Solana transaction before dialing: a malformed payload is a local
// mistake, not a chain outage.
func TestSubmitRejectsMalformedPayload(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	for name, payload := range map[string][]byte{
		"empty":   nil,
		"garbage": {0xff, 0xff, 0xff},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := n.Submit(context.Background(), payload); err == nil {
				t.Fatal("Submit over a malformed payload: err = nil, want error")
			}
		})
	}
	if f.callCount("send_transaction") != 0 {
		t.Error("a malformed payload must not dial the chain")
	}
}

// TestSubmitTimeoutStaysOpen leaves the outcome open on a hung endpoint:
// the transaction may still have been accepted, so the caller
// reconciles instead of assuming it was not sent.
func TestSubmitTimeoutStaysOpen(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.fail("send_transaction", netErr{msg: "read: i/o timeout", timeout: true})
	n, signed := signedFixture(t, f)

	if _, err := n.Submit(context.Background(), signed); err == nil {
		t.Fatal("Submit over a dead endpoint: err = nil, want error")
	} else if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.RequestTimeout {
		t.Errorf("err = %v, want request timeout (the outcome stays open)", err)
	}
}
