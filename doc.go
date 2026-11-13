// Package netsec builds a complete, content-addressed source closure for a Go
// artifact host. It walks immutable commit, tree, blob, and gitlink objects;
// it never reads a checked-out working tree. The caller supplies a trust
// manifest that maps gitlink paths to repository identities and trusted commit
// roots. Any missing object, digest mismatch, undeclared gitlink, untrusted
// submodule commit, path collision, repository/commit cycle, or submodule
// deeper than eight edges fails the entire build.
package netsec
