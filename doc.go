// Package netsec validates path coverage for Tetragon tracing policies before
// they are saved or distributed.
//
// Tetragon matches the raw arguments passed to a traced kernel function. A
// policy that hooks only openat, matches a path differently from the way an
// attacker spells it, or relies on Postfix for a directory can leave the same
// protected target reachable through a sibling call or a relative path.
//
// Validate (or Analyze when an error return is inconvenient) combines a list
// of policy Hooks, the protected Targets, and a same-family FamilyTable. It
// reports one Gap for every target and call that remains uncovered, plus
// Issues for structural rejection and for unsound selector or action forms.
//
// The package performs static validation; it does not attach probes, resolve
// dirfds, or query the kernel.
package netsec
