// Package graphdec decodes a length-prefixed binary object-graph format
// under strict, jointly enforced resource limits.
//
// The format is pure data: it carries no type names, no code, and no
// callbacks, so decoding never executes host code. Every length field is
// validated against the remaining input and the configured budgets before
// any allocation is made.
//
// Wire format (all integers are unsigned varints unless noted):
//
//	stream := magic rootID nodeCount node...
//	magic  := "OGD1"                       // 4 bytes
//	rootID := uvarint                      // id of the root node
//	nodeCount := uvarint                   // distinct nodes; shared nodes appear once
//	node   := id kind payload
//	id     := uvarint                      // unique within the stream
//	kind   := byte                         // see Kind constants
//	payload:
//	  KindNull   => (empty)
//	  KindBool   => 1 byte (0/1)
//	  KindInt    => varint (zig-zag signed)
//	  KindString => uvarint len, len bytes (UTF-8)
//	  KindBytes  => uvarint len, len bytes
//	  KindList   => uvarint n, n x uvarint childID
//	  KindMap    => uvarint n, 2n x uvarint (keyID valueID)*
//
// Node ids may appear in any order; references to not-yet-declared ids are
// staged as pending forward references (bounded by Limits.MaxPendingRefs)
// and must all resolve by the end of the stream. Trailing bytes are
// rejected.
//
// Decoding is all-or-nothing: the graph is parsed into a private staging
// area, fully validated (structure, references, depth, quotas) by an
// independent analyzer, and only then published. An error never leaks a
// partially decoded object.
//
// Graph depth is defined as the longest path, in nodes, of the
// condensation DAG obtained by contracting every strongly connected
// component reachable from the root. Cycles therefore never inflate
// depth; nodes that participate in cycles are instead bounded by the
// separate Limits.MaxCycleNodes quota, so the result never depends on
// DFS traversal order.
package graphdec
