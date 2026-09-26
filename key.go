package tailsampler

import "hash/fnv"

// DecisionKey derives the sampling decision key from the trace ID alone.
// It is deterministic, so spans of the same trace map to the same key no
// matter which shard or process receives them.
func DecisionKey(traceID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(traceID))
	return h.Sum64()
}

// ShardFor routes a trace to a shard deterministically. The same trace always
// lands on the same shard, and any shard can recompute the decision key from
// the trace ID without coordination.
func ShardFor(traceID string, shards int) int {
	if shards <= 0 {
		return 0
	}
	return int(DecisionKey(traceID) % uint64(shards))
}
