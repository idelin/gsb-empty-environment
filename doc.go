// Package netsec computes capability diffs between install plans for an
// admission host. An artifact upgrade may introduce install hooks that widen
// file and network permissions; Diff compares the old and new install plan
// ASTs and reports, per condition assignment, the capabilities the new plan
// adds and the paths it can feasibly touch.
//
// Plans contain only reads and writes of paths, calls to trusted functions,
// conditions, and fixed-count loops. Path identity is defined by a
// caller-supplied normalization mapping. With at most MaxConditions boolean
// conditions, an enumeration interpreter evaluates every assignment, so
// dangerous operations that are unreachable are verified as such and never
// counted as actual additions.
package netsec
