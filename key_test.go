package tailsampler

import "testing"

func TestDecisionKeyConsistentAcrossShards(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	key := DecisionKey(traceID)

	// The key depends only on the trace ID: every shard recomputes the same
	// value, and every span of the trace shares it.
	for _, shards := range []int{1, 2, 4, 8, 16, 64} {
		_ = ShardFor(traceID, shards)
		if DecisionKey(traceID) != key {
			t.Fatalf("key changed with shard count %d", shards)
		}
	}
	if ShardFor(traceID, 8) != ShardFor(traceID, 8) {
		t.Fatal("shard routing is not deterministic")
	}
	if ShardFor(traceID, 0) != 0 {
		t.Fatal("zero shards should route to 0")
	}
	if DecisionKey(traceID) == DecisionKey(traceID+"x") {
		t.Fatal("different trace IDs must not collide here")
	}
}
