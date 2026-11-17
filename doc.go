// Package netsec implements the page lifetime boundary for a virtual device
// host whose DMA engine can still access memory after a mapping is revoked.
//
// The central rule is that a page may return to the ordinary allocation pool
// only when no accepted DMA operation can still reference it. A Mapping pins
// pages and tracks generation-scoped operations. Revocation stops new
// submissions, but outstanding acknowledgments must all arrive before pages are
// released. A device reset can release pages sooner only when it presents an
// opaque StopProof showing that the entire DMA queue halted and fenced every
// accepted access.
//
// Completion is idempotent, so duplicate or late completion reports cannot
// decrement the outstanding count twice and trigger premature page reuse.
package netsec
