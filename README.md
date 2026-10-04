# byte-radix-tree

Compressed radix tree (Patricia trie) over byte-string keys, written in
TypeScript with zero runtime dependencies.

## Features

- **Update**: `set` inserts or replaces; `get` / `has` for point lookups.
- **Prefix enumeration**: `entries(prefix)` / `iterator(prefix)` yield all
  entries whose key starts with `prefix`, in unsigned byte lexicographic
  order. The prefix may end in the middle of an edge label.
- **Longest common prefix**: `longestCommonPrefix(key)` returns the longest
  byte string that is a prefix of `key` and of at least one stored key.
- **Delete**: `delete` removes a key; non-terminal single-child nodes are
  merged back into their parent edge, terminal (value-carrying) nodes are
  always preserved.
- **Byte-string keys**: keys are `Uint8Array`; the empty key is allowed and
  lives on the root. Edge labels are always non-empty.
- **Versioned iterators**: the tree is persistent (copy-on-write). Every
  mutation bumps `version`; iterators and `snapshot()` views capture the
  version they were created on and keep traversing that exact state no
  matter how the tree is mutated afterwards.
- **Pagination**: `RadixIterator.nextPage(limit)` returns resumable pages in
  byte lexicographic order; a short or empty page means exhaustion.

## Usage

```ts
import { RadixTree } from './src/radix-tree.ts';

const enc = new TextEncoder();
const tree = new RadixTree<number>();

tree.set(enc.encode('romane'), 1);
tree.set(enc.encode('romanus'), 2);
tree.set(enc.encode('rom'), 3);      // splits the "romane" edge in the middle
tree.set(new Uint8Array(0), 0);      // empty key is allowed

tree.longestCommonPrefix(enc.encode('romulus')); // -> "rom"

const it = tree.iterator(enc.encode('rom'));     // bound to current version
const page = it.nextPage(2);          // [{key: "rom", ...}, {key: "romane", ...}]

tree.delete(enc.encode('romane'));    // merges single-child nodes, keeps terminals
it.nextPage(2);                       // still traverses the old version

const snap = tree.snapshot();         // immutable read-only view of a version
for (const { key, value } of snap.entries()) { /* ... */ }
```

## Invariants

`tree.checkInvariants()` verifies the structural invariants (non-empty edge
labels, no unmerged non-terminal single-child nodes, sorted keys, consistent
size) and throws on violation. The test suite cross-checks every operation
against a sorted-key-set reference model, including edge mid-splits, prefix
key deletion, and continued traversal on snapshots.

## Tests

Requires Node.js >= 22.6 (TypeScript type stripping) or any setup that can
run TypeScript directly:

```sh
npm test
# or explicitly:
node --test "test/*.test.ts"
```
