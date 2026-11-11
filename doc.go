// Package netsec provides a page-alias protection policy for a safe debugger
// host.
//
// The debugger can map the same physical page at several virtual addresses
// ("aliases"). The policy enforces that, for every physical page, no writable
// alias coexists with an executable alias: a read-only code page must never be
// writable through another virtual address.
//
// Mapping transactions (map/unmap/reprotect) are compiled into an ordered
// sequence of driver steps. All permission decreases are applied before any
// permission increase, so every intermediate state still satisfies the
// invariant. A driver may fail at any step; the engine keeps the last known
// good state and reports forward (resume) and backward (rollback) recovery
// plans.
package netsec
