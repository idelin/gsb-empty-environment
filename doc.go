// Package netsec statically detects prototype pollution in a restricted
// script language before the script is executed.
//
// Programs allocate objects, alias variables, manipulate string values
// (including attacker-controlled strings), read and write fields, branch, and
// run bounded loops. Analyze performs flow-sensitive, heap-sensitive alias
// and string-set propagation and reports writes that may reach the shared
// prototype. Each finding carries a data-flow witness and a MustWrite or
// MayWrite certainty.
//
// Execute provides matching concrete semantics. CheckSoundness exhaustively
// enumerates external string values for a small program and verifies that no
// concretely reachable dangerous write is missing from the static report.
package netsec
