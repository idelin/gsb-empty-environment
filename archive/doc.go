// Package archive implements the read side of a read-only compressed object
// service.
//
// An archive is described by a Manifest.  The manifest is a small DAG of nodes
// that describe how the expanded byte stream (called the root coordinate space)
// is assembled from physical blocks:
//
//	Raw      a whole physical block
//	Dict     a slice of a physical block (a dictionary slice)
//	Slice    a slice of a child
//	Reverse  the reverse of a child
//	ReverseSlice  a slice of a child emitted backwards
//	Repeat   a child repeated a number of times
//	Concat   children emitted in sequence
//
// Physical blocks are the unit of decode work and the unit held by a PageCache.
// Expanded byte positions are the unit of authorization and of returned-data
// accounting.  The two accounts are deliberately kept separate:
//
//   - DecodeBlocks counts physical blocks that were actually handed to the
//     decoder (cache hits do not count).
//   - ReturnBytes counts the union of requested root intervals.  Overlapping
//     requests are counted once in this account even though each request still
//     receives its own output buffer.
//
// Authorization is checked against root coordinates before any decoder
// callback is invoked.  A decoded page may physically contain bytes adjacent
// to an authorized slice, and may be shared by several manifest references,
// but materialization only ever copies the exact authorized/requested block
// offsets.  Nothing can grow a read through a reverse-slice or repeated-subtree
// boundary.
//
// Planning a batch of intersecting reads is an offline cache problem.  The
// planner may reorder physical work (emission order) but results are always
// returned in the caller's request order.  For small batches it exhaustively
// enumerates emission orders and uses Belady replacement; for larger batches
// it runs a bounded exact state search over (emitted set, resident page set)
// and only falls back to a heuristic when that state space is too large.  The
// acceptance tests compare the small-batch result with an independent full
// state-space search.
package archive
