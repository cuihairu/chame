package repository_test

import (
	"testing"

	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
	repository "github.com/cuihairu/chame/tests/conformance/repository"
)

// TestMemoryStoreConformance runs the shared persistence suite against
// the in-memory store (ADR-0002): the SQL implementation must pass the
// same suite before it ships.
func TestMemoryStoreConformance(t *testing.T) {
	repository.Run(t, func(t *testing.T) transaction.Repository {
		return memory.New()
	})
}
