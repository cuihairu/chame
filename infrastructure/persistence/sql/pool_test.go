package sql

import "testing"

// The pool configuration normalizes to a workable pool: a sized pool
// keeps idle connections matched to open (database/sql's default of 2
// would churn connections under any real burst), an oversized idle is
// clamped, and a negative idle disables idle retention.
func TestPoolConfigDefaults(t *testing.T) {
	got := PoolConfig{}.withDefaults()
	if got.MaxOpen != DefaultMaxOpenConns || got.MaxIdle != DefaultMaxOpenConns {
		t.Fatalf("zero = %+v, want %d open and %d idle", got, DefaultMaxOpenConns, DefaultMaxOpenConns)
	}
	got = PoolConfig{MaxOpen: 3}.withDefaults()
	if got.MaxOpen != 3 || got.MaxIdle != 3 {
		t.Fatalf("open only = %+v, want idle matched to 3", got)
	}
	got = PoolConfig{MaxOpen: 8, MaxIdle: 2}.withDefaults()
	if got.MaxOpen != 8 || got.MaxIdle != 2 {
		t.Fatalf("explicit = %+v, want 8/2", got)
	}
	got = PoolConfig{MaxOpen: 4, MaxIdle: 9}.withDefaults()
	if got.MaxIdle != 4 {
		t.Fatalf("idle above open = %+v, want clamped to 4", got)
	}
	got = PoolConfig{MaxOpen: 4, MaxIdle: -1}.withDefaults()
	if got.MaxIdle != 0 {
		t.Fatalf("negative idle = %+v, want 0", got)
	}
}
