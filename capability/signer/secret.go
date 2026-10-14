package signer

// Secret carries key material through the system without letting it leak
// into logs, errors, or serialized configuration (spec §12.3: 生产日志
// 不得记录私钥、助记词或完整秘密材料；配置系统不应把密钥秘密作为普通
// 字符串到处复制). Every rendering path — String, GoString, and text or
// JSON marshaling — is redacted; only Bytes exposes the material, to the
// signer implementations that must use it. Configuration that wants to
// reference a key does so by environment variable name or by wiring, not
// by carrying a Secret literal.
type Secret struct {
	raw []byte
}

// NewSecret copies the material: later mutation of the argument does not
// change the secret.
func NewSecret(b []byte) Secret {
	raw := make([]byte, len(b))
	copy(raw, b)
	return Secret{raw: raw}
}

// Bytes exposes the key material to signer implementations. Treat the
// result as read-only; it aliases the stored bytes.
func (s Secret) Bytes() []byte { return s.raw }

// IsZero reports whether no material is held.
func (s Secret) IsZero() bool { return len(s.raw) == 0 }

// Len reports the byte length without exposing the material.
func (s Secret) Len() int { return len(s.raw) }

// String is redacted so fmt and slog never render key material.
func (s Secret) String() string { return "[redacted]" }

// GoString is redacted so %#v never renders key material.
func (s Secret) GoString() string { return "[redacted]" }

// MarshalText is redacted so JSON and YAML dumps never carry key
// material.
func (s Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }
