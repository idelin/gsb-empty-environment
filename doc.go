// Package nvmerecov recovers lost NVMe Zone Append completion receipts from a
// migration breakpoint.
//
// The input is a set of ordered submission-queue programs, cross-queue
// barriers, zone capacities, and the breakpoint evidence. The solver replays
// legal execution prefixes symbolically. It never expands long run-length
// encoded zone images and never merges distinct request IDs merely because
// their appended content is identical.
//
// Analyze answers whether all evidence has one legal explanation and reports,
// for every request without a surviving receipt, the exact set of possible
// outcomes: unexecuted, or executed results deduplicated by their real return
// value. Each reported outcome carries a prefix that can be replayed on its
// own. QueryJoint checks a batch of hypothetical receipts against one shared
// prefix, so individually possible but mutually incompatible assumptions are
// rejected.
//
// The empty marker represented by Block.Empty is distinct from a written block
// whose byte value is empty. Read results and zone images are compared with
// exact byte content, not hashes.
package nvmerecov
