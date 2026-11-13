// Package netsec is an importable verifier for JIT safepoint stack maps.
//
// It models a finite SSA function with pointer and non-pointer values,
// allocation sites, interior pointers, Phis, calls, safepoints, and returns.
// Analyze computes control-flow liveness to a fixed point, including Phi
// backedges and exceptional call edges. CheckStackMaps verifies candidate
// register/slot maps against that liveness and against object bounds and
// derived-base relations. Execute runs a bounded concrete interpreter with
// object generations and safepoint collection, reporting shortest paths to
// use-after-free or integer-mislabel findings.
//
// A pointer type is always an abstract object set plus a half-open offset
// range. Concrete execution additionally carries a base generation and the
// concrete offset; a raw address is never accepted as pointer provenance.
package netsec
