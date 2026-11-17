// Package netsec implements a defensive relocation writer used by a binary
// loader during its loading phase.
//
// The loader receives a segment table and a relocation table whose entries are
// fully attacker controlled. Every relocation is validated against the segment
// layout before any byte is written: targets must lie entirely inside a
// writable, non-executable segment, symbols must lie inside a readable segment
// and the integer arithmetic must stay representable. Only when all entries
// pass validation are they applied atomically; a failed Apply leaves every
// segment byte unchanged. This package never maps or executes code.
//
// All multi-byte integers are encoded little-endian.
package netsec
