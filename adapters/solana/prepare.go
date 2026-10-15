package solana

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/cuihairu/chame/capability/signer"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// AccountRef is one account an instruction touches (spec §8.2). Solana
// keeps three independent roles per account — read or write, sign or
// not — and the adapter preserves them exactly instead of guessing.
type AccountRef struct {
	Address  identifiers.Address
	Writable bool
	Signer   bool
}

// Instruction is one program call, stated chain-agnostically (spec
// §8.2). Nothing here is an SDK type: the adapter compiles these into a
// Solana message.
type Instruction struct {
	// Program is the on-chain program the call invokes.
	Program identifiers.Address
	// Data is the program's own input encoding. The adapter does not
	// interpret it; an instruction layout belongs to its program.
	Data []byte
	// Accounts lists the accounts the program reads or writes, in the
	// order the program expects.
	Accounts []AccountRef
}

// BuildQuery is the application's intent for one state-changing
// transaction (spec §8.3).
type BuildQuery struct {
	// FeePayer pays the transaction fee and signs first. Its signature
	// is the first in the list, which is also the chain's transaction
	// identity.
	FeePayer identifiers.Address
	// Instructions are the program calls of this transaction, executed
	// in order and committed atomically.
	Instructions []Instruction
	// Commitment is the level the recent blockhash is read at (spec
	// §8.5). Empty takes the network default.
	Commitment Commitment
	// Deadline bounds the whole build, not one request. Zero leaves the
	// caller's context in charge.
	Deadline time.Duration
	// IdempotencyKey is the application's key for this operation (spec
	// §10.1): the repository's unique scope, not the chain's identity.
	IdempotencyKey string
}

// Built is one prepared transaction, ready to sign and submit. The
// payload is the compiled unsigned message; every required signer signs
// those exact bytes, and the signatures travel in Signers order.
type Built struct {
	// Payload is the unsigned compiled message. ed25519 signs these
	// exact bytes; there is no separate digest step.
	Payload []byte
	// Format names the encoding, for the signer request.
	Format signer.Format
	// Signers is the required signature order, starting with the fee
	// payer. A signature list is complete only in this order.
	Signers        []identifiers.Address
	FeePayer       identifiers.Address
	Pin            BlockhashPin
	Commitment     Commitment
	Deadline       time.Duration
	IdempotencyKey string
}

// Build prepares one transaction from an intent (spec §8.3). It reads a
// recent blockhash from the chain and returns the compiled message to
// sign. It never signs and never broadcasts, so a build that fails has
// left the chain untouched. The blockhash is the transaction's lifetime:
// the caller records Built.Pin so expiry recovery (spec §8.4) has the
// window the transaction was built against.
func (n *Network) Build(ctx context.Context, q BuildQuery) (Built, error) {
	if err := q.validate(); err != nil {
		return Built{}, of(cherrs.InvalidArgument, n.opBase+".build", n.id.String(), err)
	}
	// The deadline bounds the whole build, not just one request.
	deadlineCtx := ctx
	if q.Deadline > 0 {
		var cancel context.CancelFunc
		deadlineCtx, cancel = context.WithTimeout(ctx, q.Deadline)
		defer cancel()
	}
	ctx = deadlineCtx

	// One pin per build: a fresh read, not a cached one, because the
	// hash is the transaction's deduplication and its lifetime.
	pin, err := n.LatestBlockhash(ctx, q.Commitment)
	if err != nil {
		return Built{}, err
	}
	msg, signers, err := n.compile(q, pin)
	if err != nil {
		return Built{}, err
	}
	return Built{
		Payload:        msg,
		Format:         signer.FormatSolanaV0,
		Signers:        signers,
		FeePayer:       q.FeePayer,
		Pin:            pin,
		Commitment:     q.Commitment,
		Deadline:       q.Deadline,
		IdempotencyKey: q.IdempotencyKey,
	}, nil
}

// validate checks the intent before any chain call, so a malformed query
// is cheap and never touches the endpoint.
func (q BuildQuery) validate() error {
	if err := q.FeePayer.Validate(); err != nil {
		return fmt.Errorf("fee payer: %w", err)
	}
	if q.FeePayer.Family != identifiers.FamilySolana {
		return fmt.Errorf("fee payer must be a solana address, got %s", q.FeePayer.Family)
	}
	if len(q.Instructions) == 0 {
		return fmt.Errorf("a transaction needs at least one instruction")
	}
	if q.Deadline < 0 {
		return fmt.Errorf("deadline must not be negative")
	}
	if q.Commitment != "" && !q.Commitment.Valid() {
		return fmt.Errorf("commitment %q is not a valid confirmation level", q.Commitment)
	}
	for i, ix := range q.Instructions {
		if err := ix.Program.Validate(); err != nil {
			return fmt.Errorf("instruction %d program: %w", i, err)
		}
		if ix.Program.Family != identifiers.FamilySolana {
			return fmt.Errorf("instruction %d: program must be a solana address, got %s",
				i, ix.Program.Family)
		}
		if len(ix.Data) == 0 {
			return fmt.Errorf("instruction %d: data must not be empty", i)
		}
		if len(ix.Accounts) == 0 {
			return fmt.Errorf("instruction %d: an instruction needs at least one account", i)
		}
		for j, a := range ix.Accounts {
			if err := a.Address.Validate(); err != nil {
				return fmt.Errorf("instruction %d account %d: %w", i, j, err)
			}
			if a.Address.Family != identifiers.FamilySolana {
				return fmt.Errorf("instruction %d account %d: must be a solana address, got %s",
					i, j, a.Address.Family)
			}
		}
	}
	return nil
}

// compile renders the intent into the unsigned compiled message and the
// signature order the message demands. It is pure: no chain call, no
// signing, and every address is revalidated here so a family mistake is
// an argument error rather than a decode panic.
func (n *Network) compile(q BuildQuery, pin BlockhashPin) ([]byte, []identifiers.Address, error) {
	op := n.opBase + ".build"
	hash, err := solana.HashFromBase58(pin.Blockhash)
	if err != nil {
		return nil, nil, of(cherrs.InvalidArgument, op, n.id.String(),
			fmt.Errorf("blockhash: %v", err))
	}
	feePayer, err := toPublicKey(op, q.FeePayer)
	if err != nil {
		return nil, nil, err
	}
	sdk := make([]solana.Instruction, 0, len(q.Instructions))
	for _, ix := range q.Instructions {
		program, err := toPublicKey(op, ix.Program)
		if err != nil {
			return nil, nil, err
		}
		metas := make([]*solana.AccountMeta, 0, len(ix.Accounts))
		for _, a := range ix.Accounts {
			key, err := toPublicKey(op, a.Address)
			if err != nil {
				return nil, nil, err
			}
			metas = append(metas, &solana.AccountMeta{
				PublicKey:  key,
				IsWritable: a.Writable,
				IsSigner:   a.Signer,
			})
		}
		sdk = append(sdk, compiledInstruction{program: program, data: ix.Data, accounts: metas})
	}
	tx, err := solana.NewTransaction(sdk, hash, solana.TransactionPayer(feePayer))
	if err != nil {
		return nil, nil, of(cherrs.InvalidArgument, op, n.id.String(), err)
	}
	msg, err := tx.Message.MarshalBinary()
	if err != nil {
		return nil, nil, of(cherrs.InternalError, op, n.id.String(),
			fmt.Errorf("encoding the compiled message: %v", err))
	}
	required := int(tx.Message.Header.NumRequiredSignatures)
	if required == 0 || required > len(tx.Message.AccountKeys) {
		return nil, nil, of(cherrs.InternalError, op, n.id.String(),
			fmt.Errorf("compiled message demands %d signatures for %d accounts",
				required, len(tx.Message.AccountKeys)))
	}
	signers := make([]identifiers.Address, 0, required)
	for _, key := range tx.Message.AccountKeys[:required] {
		a, err := toAddress(op, key)
		if err != nil {
			return nil, nil, err
		}
		signers = append(signers, a)
	}
	return msg, signers, nil
}

// compiledInstruction adapts one chain-agnostic call to the SDK
// instruction interface. It is unexported and stays inside the package
// (ADR-0001: no SDK type crosses the boundary).
type compiledInstruction struct {
	program  solana.PublicKey
	data     []byte
	accounts []*solana.AccountMeta
}

func (c compiledInstruction) ProgramID() solana.PublicKey { return c.program }

func (c compiledInstruction) Accounts() []*solana.AccountMeta { return c.accounts }

func (c compiledInstruction) Data() ([]byte, error) { return c.data, nil }

// Sign collects one signature per required signer and assembles the
// serialised transaction. Each signer sees the same compiled message and
// nothing else; which signer covers which account is answered by the
// signer's own allow-list, never by the caller's say-so (spec §12.3).
// A signature hole stops the transaction here: a list that is short or
// out of order is not a submittable transaction.
func (n *Network) Sign(ctx context.Context, b Built, signers ...signer.Signer) ([]byte, error) {
	op := n.opBase + ".sign"
	if b.Format != signer.FormatSolanaV0 {
		return nil, of(cherrs.InvalidArgument, op, n.id.String(),
			fmt.Errorf("payload format %q is not %s", b.Format, signer.FormatSolanaV0))
	}
	if len(b.Signers) == 0 {
		return nil, of(cherrs.InvalidArgument, op, n.id.String(),
			fmt.Errorf("the built transaction names no signers"))
	}
	if len(b.Payload) == 0 {
		return nil, of(cherrs.InvalidArgument, op, n.id.String(),
			fmt.Errorf("payload is empty"))
	}
	sigs := make([]solana.Signature, len(b.Signers))
	for i, account := range b.Signers {
		s := coveringSigner(signers, n.id, account)
		if s == nil {
			return nil, of(cherrs.InvalidArgument, op, n.id.String(),
				fmt.Errorf("no provided signer covers %s at signature %d", account, i))
		}
		signed, err := s.Sign(ctx, signer.Request{
			Network: n.id,
			Account: account,
			Type:    signer.SigSolanaTransaction,
			Payload: b.Payload,
		})
		if err != nil {
			return nil, err
		}
		sig, err := toRawSignature(signed)
		if err != nil {
			return nil, of(cherrs.InvalidArgument, op, n.id.String(), err)
		}
		sigs[i] = sig
	}
	msg, err := decodeMessage(b.Payload)
	if err != nil {
		return nil, of(cherrs.InvalidArgument, op, n.id.String(),
			fmt.Errorf("payload is not a Solana message: %v", err))
	}
	tx := solana.Transaction{Signatures: sigs, Message: msg}
	out, err := tx.MarshalBinary()
	if err != nil {
		return nil, of(cherrs.InternalError, op, n.id.String(),
			fmt.Errorf("serialising the signed transaction: %v", err))
	}
	return out, nil
}

// coveringSigner answers which of the provided signers may sign for
// account on this network, by their own account lists.
func coveringSigner(signers []signer.Signer, network identifiers.NetworkID, account identifiers.Address) signer.Signer {
	for _, s := range signers {
		for _, ref := range s.Accounts() {
			if ref.Network == network && ref.Address.Equal(account) {
				return s
			}
		}
	}
	return nil
}

// toRawSignature checks a signed result into the 64-byte Solana
// signature. ed25519 always produces 64 bytes; anything else is a
// misdeclared signer, not a signature.
func toRawSignature(signed signer.Signed) (solana.Signature, error) {
	if err := signed.Validate(); err != nil {
		return solana.Signature{}, err
	}
	if len(signed.Signature) != len(solana.Signature{}) {
		return solana.Signature{}, fmt.Errorf("signer returned a %d-byte signature, want %d bytes",
			len(signed.Signature), len(solana.Signature{}))
	}
	var sig solana.Signature
	copy(sig[:], signed.Signature)
	return sig, nil
}

// decodeMessage compiles raw message bytes back into the SDK message,
// the reverse of a build's payload rendering.
func decodeMessage(raw []byte) (solana.Message, error) {
	var msg solana.Message
	if err := msg.UnmarshalBase64(base64.StdEncoding.EncodeToString(raw)); err != nil {
		return solana.Message{}, err
	}
	return msg, nil
}

// decodeTransaction compiles raw transaction bytes back into the SDK
// transaction, the check Submit makes before dialing.
func decodeTransaction(raw []byte) (solana.Transaction, error) {
	var tx solana.Transaction
	if err := tx.UnmarshalBase64(base64.StdEncoding.EncodeToString(raw)); err != nil {
		return solana.Transaction{}, err
	}
	return tx, nil
}

// Submit broadcasts one signed transaction and returns the chain's
// signature. The outcome stays open: a timeout or a lost response does
// not mean the transaction was not accepted (spec §7.4, §14.3).
// Reconcile with TransactionStatus or Expire before deciding anything.
func (n *Network) Submit(ctx context.Context, signed []byte) (string, error) {
	// Decode first: a payload that is not a transaction is a local
	// mistake, not a chain outage, and must not reach the endpoint.
	if _, err := decodeTransaction(signed); err != nil {
		return "", of(cherrs.InvalidArgument, n.opBase+".submit", n.id.String(),
			fmt.Errorf("payload is not a Solana transaction: %v", err))
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var signature solana.Signature
	err := n.pool.call(ctx, n.id.String(), n.opBase+".send_transaction", func(rc rpcClient) error {
		var e error
		signature, e = rc.SendTransaction(ctx, signed)
		return e
	})
	if err != nil {
		n.stats.countError()
		return "", err
	}
	if signature.IsZero() {
		// A node that accepted a transaction names its signature. An
		// empty answer is not a success we can record.
		n.stats.countError()
		return "", of(cherrs.InternalError, n.opBase+".submit", n.id.String(),
			fmt.Errorf("node accepted the transaction but returned no signature"))
	}
	return sigString(signature), nil
}
