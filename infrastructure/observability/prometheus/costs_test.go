package prometheus

import (
	"context"
	"testing"
)

func TestCostStoreAggregatesByIdempotencyKey(t *testing.T) {
	s := NewCostStore()
	ctx := context.Background()
	for _, c := range []Cost{{IdempotencyKey: "order-7", TransactionID: "tx-a", Network: "fake", Gas: 21000, Fee: "42"}, {IdempotencyKey: "order-7", TransactionID: "tx-b", Network: "fake", Gas: 30000, Fee: "60"}, {IdempotencyKey: "order-8", TransactionID: "tx-c", Network: "fake", Gas: 1, Fee: "1"}} {
		if err := s.Record(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.AggregateByIdempotencyKey(ctx, "order-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Transactions != 2 || got[0].Gas != 51000 || got[0].Fee != "102" {
		t.Fatalf("aggregate = %+v; want 2 transactions, 51000 gas, fee 102", got)
	}
}
