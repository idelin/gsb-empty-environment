package bp

import (
	"fmt"
	"sort"
	"strings"
)

// Operation names used in Stats.
const (
	OpRank        = "rank"
	OpSelect      = "select"
	OpExcess      = "excess"
	OpFindClose   = "findclose"
	OpFindOpen    = "findopen"
	OpEnclose     = "enclose"
	OpDepth       = "depth"
	OpParent      = "parent"
	OpFirstChild  = "first_child"
	OpNextSibling = "next_sibling"
	OpPrevSibling = "prev_sibling"
	OpChildRank   = "child_rank"
	OpSubtreeSize = "subtree_size"
	OpLCA         = "lca"
)

// Stats counts, per operation, how many queries ran and how many
// block/superblock summaries or bit blocks were touched. It is the evidence
// that queries skip whole blocks instead of scanning the parenthesis string.
type Stats struct {
	queries map[string]uint64
	blocks  map[string]uint64
}

func (s *Stats) record(op string, blockAccesses int) {
	if s.queries == nil {
		s.queries = make(map[string]uint64)
		s.blocks = make(map[string]uint64)
	}
	s.queries[op]++
	s.blocks[op] += uint64(blockAccesses)
}

// Reset clears all counters.
func (s *Stats) Reset() { s.queries, s.blocks = nil, nil }

// Queries returns the number of recorded queries for op.
func (s *Stats) Queries(op string) uint64 { return s.queries[op] }

// BlockAccesses returns the total recorded block accesses for op.
func (s *Stats) BlockAccesses(op string) uint64 { return s.blocks[op] }

// AvgBlockAccesses returns the average number of block accesses per query.
func (s *Stats) AvgBlockAccesses(op string) float64 {
	q := s.queries[op]
	if q == 0 {
		return 0
	}
	return float64(s.blocks[op]) / float64(q)
}

// Report renders a per-operation table.
func (s *Stats) Report() string {
	ops := make([]string, 0, len(s.queries))
	for op := range s.queries {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s %10s %16s %12s\n", "op", "queries", "block-accesses", "avg-blocks")
	for _, op := range ops {
		fmt.Fprintf(&b, "%-14s %10d %16d %12.2f\n",
			op, s.queries[op], s.blocks[op], s.AvgBlockAccesses(op))
	}
	return b.String()
}

// Stats returns the index's query statistics collector.
func (ix *Index) Stats() *Stats { return &ix.stats }
