// Package netsec builds a controlled environment block for a privileged
// service launcher.
//
// Only the explicit reference syntax ${NAME} is recognized inside trusted
// templates. Referenced values are substituted as opaque data and are never
// re-scanned, so an untrusted inherited value cannot smuggle new references
// into the expansion. The only escape sequence is "$$", which yields one
// literal '$' and nothing else.
//
// Build first validates the dependency graph: undefined references and
// reference cycles are rejected before any output is produced. Expansion is
// memoized so shared sub-expressions are computed once, while two budgets
// bound the work: the longest reference chain (MaxDepth) and the cumulative
// number of appended bytes, counting every copy of a shared fragment
// (MaxOutputBytes).
//
// Every output byte carries provenance identifying the trusted or untrusted
// origin it came from. A sensitive variable is rejected unless every byte of
// its fully expanded value originates from a trusted source. Empty values are
// vacuously trusted.
package netsec
