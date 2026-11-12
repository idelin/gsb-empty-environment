// Package netsec composes stacked Landlock filesystem rulesets for sandbox
// launcher processes.
//
// Landlock stacks rulesets by intersection: an action is allowed at a path
// only when every ruleset that handles the corresponding access right grants
// it. Within a single ruleset, all matching rules (including rules on
// ancestor directories) are unioned. This package models those semantics
// purely in Go so a launcher can compute the effective rights at any path
// before enforcing them.
//
// The package fails closed: when the target kernel ABI cannot represent a
// right requested in a layer's handled mask, Compose returns an
// UnsupportedAccessError instead of silently dropping the bit. A dropped
// handled right would leave that action completely unrestricted.
package netsec
