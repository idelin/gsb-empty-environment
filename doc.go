// Package netsec plans restore closures that are safe to hand to a restore
// host.
//
// Restoring a directory can indirectly restore every secret it references, so
// a directory-level grant is not sufficient. PlanRestore checks permission for
// every object in the required closure, omits optional references explicitly,
// and requires Publish to recheck current permissions before producing an
// executable manifest.
package netsec
