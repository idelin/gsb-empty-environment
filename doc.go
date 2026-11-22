// Package proofaccept checks whether a collection of local page-set
// measurements can be explained by one causal cut of a mapping history.
//
// The history is a directed acyclic graph of map, modify, remap, and revoke
// operations.  A cut is represented by the maximal events of a downward-closed
// set (its frontier), so checking never enumerates topological sorts.  The
// package also verifies explicit frontier witnesses and conflict certificates.
//
// A PageState contains every field that identifies a mapping: the backing
// frame, object identity, mapping generation, content digest, executable bit,
// and signing source.  Digest equality alone is never enough: generations are
// part of the identity, and virtual page IDs that resolve to the same backing
// frame are compared as one page.
package proofaccept
