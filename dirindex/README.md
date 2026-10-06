# dirindex

In-memory directory index that reports per-subtree disk usage with
hardlink-aware (inode) deduplication.

## Dedup model

Every subtree deduplicates independently: the usage of a directory is
the sum of the sizes of the distinct inodes that have at least one hard
link anywhere beneath it. Therefore a parent's usage is generally **not**
the sum of its children's usages — an inode linked into two sibling
subtrees is counted once at the parent but once in each child.

## Incremental maintenance

Each directory keeps an `inode -> link count` refcount map for its own
subtree plus the running deduplicated usage:

- `AddFile` / `RemoveFile` propagate one inode delta up the ancestor
  chain in O(depth); usage changes only on 0→1 / 1→0 transitions. When
  the last link in the whole index is removed, the inode is forgotten.
- `Move` collects the moved subtree's inode multiset once, subtracts it
  from every old ancestor and adds it to every new ancestor. The moved
  subtree's own summaries are untouched, and root usage is invariant.
- `Snapshot` returns an immutable point-in-time copy of every
  directory's usage.

## Verification

`ModelUsage` is the independent reference: it recomputes a directory's
usage from scratch by walking the subtree with a fresh inode set.
`Check` cross-checks every directory's incremental usage against the
model. Tests cover cross-subtree hard links, same-subtree dedup,
deleting the last link, ancestor updates on move, snapshot immutability,
and a randomized differential test (5 seeds x 600 ops, checking every
node after every step).

```sh
go test ./...
```
