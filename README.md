# compressed-radix-tree

Compressed radix tree (Patricia trie) over byte-string keys, in TypeScript.

- Keys are `Uint8Array` byte strings and may be empty; edge labels are never empty.
- `set` / `get` / `has` / `delete` with on-delete compression: non-terminal
  single-child nodes are merged, terminal markers are always preserved.
- Prefix enumeration (`entries` / `keys`) in byte-lexicographic order.
- Longest-common-prefix queries: `longestCommonPrefix(query)` (may end
  mid-edge) and `lcpOfAllKeys()`.
- `iterator(prefix?)` binds to the tree `version` at creation and walks a
  stable snapshot in pages (`nextPage(limit)`) or one by one (`for...of`);
  later mutations never affect it (`isStale()` reports drift).

## Requirements

Node.js >= 22.6 (runs TypeScript via type stripping; no build step).

## Test

```sh
node --test test/radix-tree.test.ts
```

Tests cross-check the tree against a sorted-key-set reference model, covering
edge splits in the middle, prefix-key deletion with single-child merging, and
snapshot continuation of paginated iterators across mutations.
