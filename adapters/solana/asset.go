package solana

import (
	"bytes"
	"context"
	"encoding/binary"
	"strconv"

	"github.com/gagliardetto/solana-go"

	"github.com/cuihairu/chame/application/gameledger"
	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/capability/nft"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

var _ nft.SolanaProvider = (*AssetProvider)(nil)

const metaplexMetadataProgram = "metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s"

// SolanaHolding recognizes Metaplex NFT metadata by its canonical metadata
// PDA and checks the wallet's SPL token accounts. RPC observations use this
// network's configured commitment; this is not a game-ledger finality gate.
func (p *AssetProvider) SolanaHolding(ctx context.Context, id gameledger.AssetIdentity, owner identifiers.Address) (nft.SolanaOwnership, error) {
	id, err := id.Normalize()
	if err != nil {
		return nft.SolanaOwnership{}, err
	}
	if id.Network != p.net.id || (id.Standard != gameledger.StandardSPLNFT && id.Standard != gameledger.StandardSPL) {
		return nft.SolanaOwnership{}, cherrs.New(cherrs.InvalidArgument, p.net.opBase+".nft_holding", "identity must be SPL on this cluster")
	}
	mint, err := solana.PublicKeyFromBase58(id.Contract)
	if err != nil {
		return nft.SolanaOwnership{}, err
	}
	program := solana.MustPublicKeyFromBase58(metaplexMetadataProgram)
	metadata, _, err := solana.FindProgramAddress([][]byte{[]byte("metadata"), program[:], mint[:]}, program)
	if err != nil {
		return nft.SolanaOwnership{}, err
	}
	metaAddr, _ := identifiers.NewSolanaAddress(metadata.String())
	meta, err := p.net.Account(ctx, metaAddr, "")
	if err != nil {
		return nft.SolanaOwnership{}, err
	}
	isNFT := meta != nil && meta.Owner == metaplexMetadataProgram && len(meta.Data) >= 65 && bytes.Equal(meta.Data[33:65], mint[:])
	if id.Standard == gameledger.StandardSPLNFT && !isNFT {
		return nft.SolanaOwnership{}, cherrs.New(cherrs.InvalidArgument, p.net.opBase+".nft_holding", "mint has no canonical Metaplex metadata account")
	}
	if id.Standard == gameledger.StandardSPL && isNFT {
		return nft.SolanaOwnership{}, cherrs.New(cherrs.InvalidArgument, p.net.opBase+".nft_holding", "Metaplex NFT must use spl-nft identity")
	}
	mintAddr := idAddress(id.Contract)
	accounts, err := p.net.TokenAccounts(ctx, owner, &mintAddr, "")
	if err != nil {
		return nft.SolanaOwnership{}, err
	}
	result := nft.SolanaOwnership{Owner: owner, Held: false, Classification: nft.SolanaFungibleSPL, FinalitySatisfied: false}
	if isNFT {
		result.Classification = nft.SolanaMetaplexNFT
	}
	for _, account := range accounts {
		bal, e := p.net.TokenBalance(ctx, account.Address, "")
		if e != nil {
			return nft.SolanaOwnership{}, e
		}
		if bal != nil && bal.RawAmount != "0" {
			result.Held = true
			result.TokenAccount = account.Address
			break
		}
	}
	return result, nil
}

func idAddress(s string) identifiers.Address { a, _ := identifiers.NewSolanaAddress(s); return a }

// The SPL Token Mint layout (spl_token::Mint) is fixed at 82 bytes:
// mint_authority COption at 0, supply at 36, decimals at 44,
// is_initialized at 45. The supply is a little-endian u64, as the
// layout encodes it. Offsets are decoded from the account the node
// reports; the layout is not reconstructed from any schema.
const (
	mintLayoutSize     = 82
	mintSupplyOffset   = 36
	mintDecimalsOffset = 44
	mintInitOffset     = 45
)

// AssetProvider implements capability/asset.Provider over one Solana
// cluster (spec §13.2). Read-only: no call here can produce an on-chain
// side effect.
type AssetProvider struct {
	net *Network
}

var _ asset.Provider = (*AssetProvider)(nil)

// AssetProvider exposes the asset capability for this network.
func (n *Network) AssetProvider() *AssetProvider { return &AssetProvider{net: n} }

// Network implements asset.Provider.
func (p *AssetProvider) Network() identifiers.NetworkID { return p.net.id }

// Asset reports one asset's precision and metadata (spec §13.2). The
// native asset and SPL mints carry no symbol or name: that metadata is
// not part of either account, so the empty value is reported rather
// than a guess. SPL decimals are read from the mint account the chain
// stores, never assumed.
func (p *AssetProvider) Asset(ctx context.Context, id identifiers.AssetID) (asset.AssetInfo, error) {
	if err := p.checkAsset(id); err != nil {
		return asset.AssetInfo{}, err
	}
	if id.Kind == identifiers.KindNative {
		return asset.AssetInfo{
			Asset:    id,
			Decimals: solDecimals,
			Details:  []asset.Detail{{Key: "base_unit", Value: "lamport"}},
		}, nil
	}
	op := p.net.opBase + ".asset"
	mint, err := p.mintAccount(ctx, id.Address, op)
	if err != nil {
		return asset.AssetInfo{}, err
	}
	decimals, details, err := parseMint(op, mint.Data)
	if err != nil {
		p.net.stats.countError()
		return asset.AssetInfo{}, err
	}
	return asset.AssetInfo{Asset: id, Decimals: decimals, Details: details}, nil
}

// Balance reports holder's balance of id in the asset's smallest unit,
// stamped with that asset's own precision (spec §13.2, §6.4). For an
// SPL token the holder is the token account the caller names: the
// adapter does not resolve an owner wallet to an associated token
// account, and it performs no approval or transfer.
func (p *AssetProvider) Balance(ctx context.Context, id identifiers.AssetID, holder identifiers.Address) (identifiers.Amount, error) {
	if err := p.checkAsset(id); err != nil {
		return identifiers.Amount{}, err
	}
	if _, err := toPublicKey(p.net.opBase+".asset_balance", holder); err != nil {
		p.net.stats.countError()
		return identifiers.Amount{}, err
	}
	if id.Kind == identifiers.KindNative {
		lamports, err := p.net.Balance(ctx, holder, "")
		if err != nil {
			return identifiers.Amount{}, err
		}
		return identifiers.NewAmount(strconv.FormatUint(lamports, 10), solDecimals, id.String())
	}
	op := p.net.opBase + ".asset_balance"
	tok, err := p.net.TokenBalance(ctx, holder, "")
	if err != nil {
		return identifiers.Amount{}, err
	}
	if tok != nil {
		return identifiers.NewAmount(tok.RawAmount, tok.Decimals, id.String())
	}
	// The node reports null rather than zero for an address that holds
	// no token amount, so the mint's precision is needed to express a
	// zero balance. Existence of the token account is the account
	// capability's answer, not the asset capability's.
	decimals, err := p.mintDecimals(ctx, id.Address, op)
	if err != nil {
		return identifiers.Amount{}, err
	}
	return identifiers.NewAmount("0", decimals, id.String())
}

// Healthy reports whether the network connection is usable.
func (p *AssetProvider) Healthy(ctx context.Context) error { return p.net.Healthy(ctx) }

// checkAsset validates the asset identifier and pins it to this network.
func (p *AssetProvider) checkAsset(id identifiers.AssetID) error {
	if err := id.Validate(); err != nil {
		return cherrs.New(cherrs.InvalidArgument, p.net.opBase+".asset", "%v", err)
	}
	if !id.Network.Equal(p.net.id) {
		return cherrs.New(cherrs.InvalidArgument, p.net.opBase+".asset",
			"asset network %s is not this network %s", id.Network, p.net.id)
	}
	if id.Kind != identifiers.KindNative && id.Kind != identifiers.KindSPLToken {
		return cherrs.New(cherrs.UnsupportedCapability, p.net.opBase+".asset",
			"asset kind %q is not served by the solana adapter", id.Kind)
	}
	return nil
}

// mintAccount fetches the mint account. A mint the chain has never seen
// is not an asset.
func (p *AssetProvider) mintAccount(ctx context.Context, mint string, op string) (*AccountInfo, error) {
	addr, err := identifiers.NewSolanaAddress(mint)
	if err != nil {
		p.net.stats.countError()
		return nil, cherrs.New(cherrs.InvalidArgument, op, "%v", err)
	}
	info, err := p.net.Account(ctx, addr, "")
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, cherrs.New(cherrs.InvalidArgument, op, "mint account %s does not exist", mint)
	}
	return info, nil
}

// mintDecimals reads only the precision out of a mint account.
func (p *AssetProvider) mintDecimals(ctx context.Context, mint string, op string) (uint8, error) {
	info, err := p.mintAccount(ctx, mint, op)
	if err != nil {
		return 0, err
	}
	d, _, err := parseMint(op, info.Data)
	if err != nil {
		p.net.stats.countError()
		return 0, err
	}
	return d, nil
}

// parseMint decodes the precision and the mint attributes from the SPL
// Token Mint account the node reports.
func parseMint(op string, data []byte) (uint8, []asset.Detail, error) {
	if len(data) != mintLayoutSize {
		return 0, nil, cherrs.New(cherrs.EventDecodeFailure, op,
			"mint data is %d bytes, want %d", len(data), mintLayoutSize)
	}
	supply := strconv.FormatUint(binary.LittleEndian.Uint64(data[mintSupplyOffset:mintSupplyOffset+8]), 10)
	return data[mintDecimalsOffset], []asset.Detail{
		{Key: "mint_authority", Value: mintAuthority(data[0])},
		{Key: "supply", Value: supply},
		{Key: "is_initialized", Value: strconv.FormatBool(data[mintInitOffset] != 0)},
	}, nil
}

// mintAuthority names the COption byte the mint reports for its mint
// authority.
func mintAuthority(b byte) string {
	switch b {
	case 0:
		return "absent"
	case 1:
		return "present"
	case 2:
		return "uninitialized"
	default:
		return "invalid"
	}
}
