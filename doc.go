// Package netsec provides a defensive SSZ (Simple Serialize) decoder for
// consensus-client network messages.
//
// Decoding is strict and deterministic: offset tables are fully validated
// before any sub-value is read, list and bitlist lengths are bounded by their
// type, bitlist delimiter bits and bitvector padding are checked, union
// selectors are range-checked, and no trailing or unreachable bytes are
// accepted. Malformed input is rejected through the sentinel errors declared
// in errors.go rather than producing a value that other clients might parse
// differently.
package netsec
