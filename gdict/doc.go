// Package gdict consolidates partition-local dictionaries into one global
// physical code space while preserving every listed old key lookup and range
// scan.
//
// See the repository README for the formal model, the page semantics, the
// lexicographic objective, and the conflict-certificate forms.
package gdict
