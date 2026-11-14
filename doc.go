// Package netsec implements byte-order label propagation and dangerous-range
// reporting for a small protocol-oriented IR.
//
// Values are tagged as raw bytes, big-endian integers, little-endian integers,
// order-neutral constants, conflicts, or unknown. A control-flow join uses a
// lattice join: BE and LE produce a conflict instead of selecting either arm.
// Only an explicit conversion (OpConvert) can resolve that conflict.
//
// Analyze checks every OpSlice for mixed orders, unresolved conflicts, raw
// range operands, arithmetic overflow, negative ranges, and statically known
// out-of-bounds ranges. Each Finding carries the full provenance tree of the
// slice base, offset, and length. Interpret executes the same straight-line IR
// on concrete byte buffers for positive and negative byte-order samples.
package netsec
