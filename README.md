# persistent-map-diff

Paginated, resumable diff between two versions of a persistent ordered map.
Emits `added`, `deleted` and `changed` items in ascending key order.

The map is a persistent treap (path copying, deterministic per-key priorities,
per-node Merkle digest). Untouched subtrees are shared by reference between
versions, and the diff skips them wholesale.

## Requirements

Node.js >= 23.6 runs the TypeScript sources directly (type stripping);
Node 22.6–23.5 needs `--experimental-strip-types`. Or compile with `tsc`
(`npm run typecheck` type-checks without emitting).

```sh
npm test   # node --test test/*.test.ts
```

## Quick start

```ts
import { MapStore } from 'persistent-map-diff';

const store = new MapStore<string, number>();
const v1 = store.set(store.empty(), 'a', 1);
const v2 = store.set(v1, 'b', 2);

const differ = store.differ();
let cursor = differ.beginDiff(v1, v2);   // cursor pins both roots
while (true) {
  const page = differ.nextPage(cursor, 100);
  for (const item of page.items) console.log(item);
  if (page.done) break;
  cursor = page.cursor;                  // resume token for the next page
}
```

## Cursor model

`DiffCursor = { oldRoot, newRoot, lastKey }`. The cursor **binds both version
roots and the last fully processed key** (an exclusive high-water mark).
Everything with `key <= lastKey` is done; resuming re-enters both trees at
that mark and prunes whole subtrees whose key range is already covered, so a
resumed page does not rescan completed work.

Because the cursor holds the roots itself, a diff survives `store.release()`
of the versions it compares; asking the store for a released version throws
`VersionError`.

## Skipping and the explicit hash-trust model

- Reference-equal subtrees are always skipped — identity cannot collide.
- `trust: 'trusted'` also skips subtrees with equal content digests. Fast,
  but a digest collision can hide real differences. Only use it with a
  collision-resistant `hashNode`.
- `trust: 'verified'` (default) never trusts digest equality: equal digests
  still force a full descent and entry-by-entry comparison, so a collision
  cannot change the result. Digests then serve only as a cheap "definitely
  different" signal.

The digest function is injectable (`hashNode` in the store config), which the
tests use to force collisions and pin down both behaviors.

## Paging and cancellation

- `pageSize` bounds the number of **emitted items** per page, never the
  number of keys scanned. Filling a page may require scanning (and skipping)
  arbitrarily many shared keys.
- A page may be **empty** (`items: []`, `done: false`) when cancellation hits
  before any output; the returned cursor is still a valid resume point.
- Cancellation is cooperative: pass `shouldCancel()` and/or a `scanBudget`
  (max visited nodes) in the `ScanControl` argument. The scan unwinds
  immediately and the page's cursor resumes exactly where it stopped.
- `stats.scannedNodes` per page makes the scan/output asymmetry observable.

## Reference implementation

`mergeDiff(oldEntries, newEntries, compare)` is a plain full merge join over
two sorted entry streams. The test suite cross-checks the paginated engine
against it under randomized maps, page sizes and cancellation points, plus
targeted cases: empty maps, empty pages, long common prefixes, released
versions and forced hash collisions.

## Complexity

Let `d` be the number of differing entries and `n` the map size. The scan
visits `O(d log n)` tree nodes thanks to structural sharing; a full merge is
`O(n)`. Resume after cancellation costs one `O(log n)` re-descent per page.
