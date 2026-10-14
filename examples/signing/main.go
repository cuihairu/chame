// Command signing demonstrates the signer capability (spec §12): a
// deterministic test signer, allow-list authorization, and audited
// signing attempts. It needs no chain access and no key material.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cuihairu/chame/adapters/evm"
	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

func main() {
	network := identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}
	// The allow-list is the whole authorization story (§12.3): a module
	// can only sign for a (network, account) pair someone wired here.
	account, err := identifiers.NewEVMAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	if err != nil {
		fmt.Fprintln(os.Stderr, "address:", err)
		os.Exit(1)
	}
	ref, err := signer.NewAccountRef(network, account)
	if err != nil {
		fmt.Fprintln(os.Stderr, "account ref:", err)
		os.Exit(1)
	}

	testSigner, err := signer.NewTestSigner("login-signer", ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signer:", err)
		os.Exit(1)
	}

	// Every signing attempt — allowed or denied — becomes one audit
	// entry with a payload digest instead of the payload (§12.3).
	audited := signer.WithAudit(testSigner, func(e signer.AuditEntry) {
		slog.Info("signing attempt",
			slog.String("signer", e.Signer),
			slog.String("network", e.Network.String()),
			slog.String("account", e.Account.String()),
			slog.String("type", string(e.Type)),
			slog.String("digest", e.Digest),
			slog.String("denied", e.Denied),
		)
	}, nil)

	ctx := context.Background()
	msg := []byte("login nonce: 2026-10-15T12:00:00Z")

	signed, err := audited.Sign(ctx, signer.Request{
		Network: network, Account: account,
		Type: signer.SigEVMMessage, Payload: msg,
	})
	fmt.Printf("allowed request:  err=%v signature=%x signer=%s\n", err, signed.Signature, signed.Signer)

	// A request naming an account outside the allow-list is denied:
	// explicit targets, no implicit rights (§12.3).
	stranger, _ := identifiers.NewEVMAddress("0x0000000000000000000000000000000000000001")
	_, err = audited.Sign(ctx, signer.Request{
		Network: network, Account: stranger,
		Type: signer.SigEVMMessage, Payload: msg,
	})
	fmt.Printf("stranger request: err=%v\n", err)

	// The EVM development signer signs EIP-191 personal messages with a
	// key held in memory. It refuses production environments, and its
	// signatures verify with evm.VerifyPersonalMessage — the shape game
	// login flows use. Real deployments bring their own implementation
	// of the same contract: an external signing service, a KMS, or an
	// HSM (§12.2).
	demoSignerFlow(network)

	time.Sleep(50 * time.Millisecond) // let slog flush the audit lines
}

// demoSignerFlow signs with a randomly generated throwaway key, so the
// example ships no key material (§12.4: no plaintext keys in examples).
func demoSignerFlow(network identifiers.NetworkID) {
	key, err := evm.GenerateDevKey()
	if err != nil {
		fmt.Println("dev key:", err)
		return
	}
	// The allow-list is built from the key itself: a held key that is
	// not listed cannot sign at all.
	account, err := evm.DevKeyAddress(key)
	if err != nil {
		fmt.Println("dev address:", err)
		return
	}
	ref, err := signer.NewAccountRef(network, account)
	if err != nil {
		fmt.Println("dev ref:", err)
		return
	}
	s, err := evm.NewLocalDevSigner(config.EnvDevelopment, key, ref)
	if err != nil {
		fmt.Println("dev signer:", err)
		return
	}
	ctx := context.Background()
	signed, err := s.Sign(ctx, signer.Request{
		Network: network, Account: account,
		Type: signer.SigEVMMessage, Payload: []byte("dev message"),
	})
	if err != nil {
		fmt.Println("dev sign:", err)
		return
	}
	ok, err := evm.VerifyPersonalMessage(account, []byte("dev message"), signed.Signature)
	fmt.Printf("dev signer:       err=%v verified=%v\n", err, ok)
}
