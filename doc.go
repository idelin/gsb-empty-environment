// Package netsec verifies that decoded GNU and System V symbol hash tables
// expose exactly the same exports as each other and as the linear symbol name
// table.
//
// Inputs are untrusted. Verification rejects malformed array lengths,
// out-of-range indexes, chain cycles and overlaps, truncated chains,
// hash/bucket mismatches, hidden exports, rebound exports, and excessive work.
// GNU tables are checked from SymOffset onward, while System V tables are
// checked for every nonempty symbol.
package netsec
