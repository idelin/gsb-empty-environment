// Package taskpkg implements an audit-commit component for a Go operations
// host.
//
// A sensitive operation is only allowed to produce its side effect after the
// component has (1) reserved a sequence from an injected, non-rollbackable
// monotonic counter, (2) obtained a signed confirmation from a trusted remote
// witness over the operation digest, subject, prior commitment and sequence,
// and (3) locally verified that signature and persisted the audit entry.
//
// The package deliberately does NOT promise availability under a network
// partition: when the witness or the monotonic counter cannot be reached the
// component fails closed and the side effect is never executed.
package taskpkg
