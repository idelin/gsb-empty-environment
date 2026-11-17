// Package netsec manages DMA completion-ring generations for Go driver
// hosts.
//
// A device completion ring reuses slots, so a stale completion entry can
// release a buffer that a new tenant is already using. This package binds
// every queue slot to a (submission sequence, buffer lease, tenant) triple
// at submit time. The hardware only reports the low bits of the sequence
// (a truncated tag); the ring reconstructs the full sequence under the
// rule that at most half of the tag space is ever in flight. Ambiguous
// completions isolate the queue instead of guessing the most recent
// request, and every lease is released exactly once. When the submission
// sequence space is exhausted the queue is drained before switching epoch.
package netsec
