package objectgraph

// Limits bounds every resource a single Decode (or Decoder.Decode) call may
// consume. Limits are checked before the corresponding allocation happens, so
// hostile input cannot trigger a large allocation merely by declaring one.
//
// The four budgets are independent and all enforced:
//
//   - MaxDepth is the longest path (number of SCC nodes) in the root-reachable
//     SCC condensation DAG. Cycles do not inflate this number.
//   - MaxNodes counts distinct reachable entities. A node shared by many edges
//     is counted once.
//   - MaxCyclicNodes is a separate quota for the number of distinct entities
//     that belong to non-trivial SCCs (cycles), independent of MaxNodes.
//   - MaxRefs counts every edge (array element or object field value), so a
//     small byte payload that fabricates many references is still bounded.
//   - MaxBytes bounds the encoded input size.
//   - MaxPendingRefs bounds how many forward references may be staged at once
//     during incremental decoding.
type Limits struct {
	MaxBytes       int
	MaxNodes       int
	MaxRefs        int
	MaxDepth       int
	MaxCyclicNodes int
	MaxPendingRefs int
	// MaxField/Element length sanity caps for declared container sizes.
	MaxContainerLen int
}

// DefaultLimits returns conservative defaults suitable for untrusted input.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:        16 << 20, // 16 MiB
		MaxNodes:        1 << 16,  // 65,535 entities
		MaxRefs:         1 << 17,  // 131,071 edges
		MaxDepth:        1024,     // condensation DAG path length
		MaxCyclicNodes:  4096,
		MaxPendingRefs:  4096,
		MaxContainerLen: 1 << 15,
	}
}

func (l Limits) valid() bool {
	return l.MaxBytes > 0 &&
		l.MaxNodes > 0 &&
		l.MaxRefs >= 0 &&
		l.MaxDepth > 0 &&
		l.MaxCyclicNodes >= 0 &&
		l.MaxPendingRefs >= 0 &&
		l.MaxContainerLen > 0
}
