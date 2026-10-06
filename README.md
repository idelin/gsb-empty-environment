# objtree

Persistent (immutable) object tree for Go with structurally shared updates
and multi-path transactions.

## Model

A `Node` carries an optional `Value` and a `Children` map. Every update
returns a new root; the old root is never mutated and stays usable as a
snapshot.

## Single-path updates

`Set(root, path, value)` and `Delete(root, path)` copy only the ancestors
of `path`. Every other subtree is shared by pointer with the previous
version, so identity (`==`) of untouched nodes is preserved. Deleting a
missing path returns the same root pointer.

## Multi-path transactions

`Apply(root, ops...)` first merges all operation paths into a single path
trie, then rewrites the tree in one pass: each shared ancestor is copied
exactly once, so concurrent updates under a common ancestor cannot
overwrite each other.

Conflict rules (rejected with `ErrConflict`):

- a delete whose path equals or is an ancestor of a set's path
  (deleting a subtree while modifying a path inside it);
- two sets on nested paths (the outer set would discard the inner one).

Folded instead of rejected: same-kind repeats on the identical path
(last one wins) and deletes subsumed by an ancestor delete.

An empty transaction returns the input root unchanged (same pointer).

## Testing

Tests verify every result against a deep-copy reference model
(`refmodel_test.go`), check object identity of all untouched subtrees,
and cover snapshot isolation and empty transactions, including a
randomized property test (`TestApplyMatchesReferenceModel`).
