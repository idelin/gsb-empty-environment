// Package netsec contains the implementation for UID 1232.
//
// It provides a bit-vector analyzer that hunts length-calculation bugs
// (e.g. integer truncation leading to undersized allocations) in binary
// decoders. The host builds a loop-free SSA graph with Builder using
// 8/16/32-bit unsigned add, multiply, truncate, extend, compare and
// branch operations, marks allocation sizes with Alloc and subsequent
// write counts with Write, and calls Analyze. Analyze reports Safe,
// Vulnerable (with a concrete input of at most 8 bytes that satisfies
// the path conditions and triggers the out-of-bounds write), or Unknown
// when the configurable search budget is exhausted.
package netsec
