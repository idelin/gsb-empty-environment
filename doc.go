// Package netsec provides the migration mapping used by a Go install host when
// a private module source moves to a new domain.
//
// Approvals are deliberately narrow. Every rule binds together:
//
//   - the legacy source identity (hostname plus optional instance ID),
//   - a package-name scope (exact path or segment-bounded prefix),
//   - an explicit semver version interval, and
//   - the new private host with a pinned certificate fingerprint.
//
// Because a public request cannot present the private source identity, an
// approval for a private name never extends to the same-named public package.
// Resolution runs against one immutable Snapshot produced by Compile, which
// enumerates every concrete version in a finite version domain: overlapping
// rules must converge on one target or compilation fails naming the specific
// conflicting versions, explicit revocations are enforced, and a request with
// no covering rule (or a version outside the domain) fails closed instead of
// falling back to a public proxy.
package netsec
