// Package capability names the replaceable chain capabilities (spec §6.1).
// Modules declare which capabilities they require and which they provide;
// the runtime validates coverage before start. Detection of a concrete
// implementation happens through ordinary Go interface assertions on the
// registered value.
package capability

// Name identifies a capability slot in the registry. Names are stable and
// lowercase; a capability name denotes a contract, not an implementation.
type Name string

// Well-known capability families from spec §6.1. An adapter may support a
// subset; unsupported capabilities must be reported as absent, never as
// silently broken calls.
const (
	Network     Name = "network"
	Account     Name = "account"
	Asset       Name = "asset"
	Transaction Name = "transaction"
	Event       Name = "event"
	Finality    Name = "finality"
	Signer      Name = "signer"
	Contract    Name = "contract"
)

// Valid reports whether the name is usable in declarations.
func (n Name) Valid() bool {
	if n == "" || len(n) > 63 {
		return false
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}
