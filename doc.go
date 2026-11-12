// Package netsec resolves security labels for a forest of cloud resources.
//
// Every node keeps three independent pieces of state per tag key:
//
//   - an explicit tag value defined directly on the node;
//   - a cached inherited value with provenance describing its ancestor chain;
//   - a cut-off marker that blocks inheritance of a key from ancestors.
//
// Resolution rules:
//
//   - an explicit value always wins and is never derived from a cache;
//   - a cut-off marker stops an inherited key from penetrating the subtree;
//   - deleting an explicit value restores inheritance for the key, while a
//     cut-off marker keeps blocking it;
//   - mutating a node only re-resolves the closure rooted at that node, so
//     unrelated subtrees keep their cached provenance untouched;
//   - moving a subtree re-resolves inherited caches inside it, and stale
//     inherited values are never promoted to explicit tags;
//   - snapshots are immutable and keep interpreting provenance against the
//     paths captured when the snapshot was taken.
package netsec
