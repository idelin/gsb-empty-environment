// Package netsec verifies that MAC tag comparisons do not choose memory
// accesses based on the contents of their inputs.
//
// A comparison under test receives ByteView values instead of raw byte
// slices. Every ByteAt call is therefore observable. The verifier runs
// comparisons with equal lengths, unequal lengths, and empty inputs, then
// compares the resulting access sequences for a fixed pair of lengths.
// Lengths are treated as public metadata; within one length pair, the access
// sequence must not change as the input contents change.
package netsec
