// Package netsec implements copy-on-write page ownership for snapshot
// sandboxes that hold shared secret pages.
//
// The ownership rules enforced here are:
//
//   - Forked snapshots share physical pages as read-only and add one
//     authorized reference per shared page.
//   - A write to a shared page allocates a private copy first; the original
//     page and every other authorized snapshot stay untouched.
//   - A page is detached for zeroing only when its last authorized reference
//     is released, so zeroing can never corrupt a still-authorized snapshot.
//   - Detached pages are queued behind a Zeroer and return to the free list
//     only after being successfully wiped. If zeroing fails (or the zeroer
//     reports success without wiping), the page is quarantined and is never
//     reallocated, so stale secret bytes cannot leak into a new allocation.
//
// The main entry points are NewPool (fixed-capacity physical page pool),
// NewHost (snapshot ownership layer), Snapshot.Fork via Host.Fork,
// Snapshot.Write (copy-on-write) and Snapshot.Revoke.
package netsec
