// Package budget implements a streaming, importable unfold budget for nested
// container trees consumed by the attachment preview service.
//
// The caller supplies a tree of nodes. Each node is either a byte stream
// (leaf with a Decode callback) or a container holding child nodes. Decode
// callbacks are treated as hostile: a callback may try to emit far more bytes
// than it declared, and its declared size is never trusted.
//
// Four independent budgets are enforced simultaneously:
//
//   - total bytes actually decoded across every leaf reference,
//   - depth of every root-to-leaf path,
//   - number of leaf references visited,
//   - number of concurrently live staging buffers.
//
// A leaf callback may only emit bytes through the controlled io.Writer handed
// to it. Every Write is checked against the byte budget before any byte is
// accepted. When a limit is violated the write is rejected up front, the
// context is cancelled and every descendant / sibling stream is torn down.
// Shared nodes are charged on every reference, cyclic references are rejected
// immediately, and results are published only after the whole tree succeeds;
// on any failure every staged buffer is reclaimed and nothing is published.
package budget
