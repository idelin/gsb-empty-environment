// Package netsec classifies finite-SSA local objects into the safe and unsafe
// compiler stacks.
//
// Analyze performs a field-sensitive, monotone alias/escape fixed point over
// the operations AddrOf, GEP, Phi, Select, StorePtr, LoadPtr, PassCall,
// PtrToInt, IntToPtr and ReturnPtr.  GEP constant offsets are kept as field
// paths; bounded variable indices remain interval-sensitive; an unbounded
// index collapses to the whole object.  Pointers stored in local memory are
// available to later LoadPtr operations with the same alias class.
//
// Call summaries are per argument: NoCapture, ReadOnly and an inclusive
// MaxOffset.  An absent summary is conservative (capture plus arbitrary
// access).  If function bodies are provided, recursive summaries are inferred
// by a monotone fixed point.  Budget exhaustion makes every managed object
// unsafe.
//
// Audit compares a pending classifier against the reference model and returns
// the shortest provenance path for every false-safe object.  SimulateLayout
// extends out-of-bounds writes into an externally supplied layout and reports
// an impact only when control metadata or another safe object is reached.
// Control metadata is never assigned an offset by this package.
package netsec
