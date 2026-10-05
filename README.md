# pmap-diff

Paginated diff between two versions of a persistent ordered map, in
TypeScript. Emits `added` / `removed` / `changed` items in ascending key
order, skips shared subtrees wholesale, and resumes from a cursor that
binds both version roots plus the last processed key.

## Pieces

- `src/avl.ts` — `PMap`: persistent AVL tree (path-copying updates,
  structural sharing between versions). Every node caches a content
  `hash` summarizing its whole subtree.
- `src/store.ts` — `VersionStore`: pins snapshots behind opaque `Version`
  handles; `release()` drops them. Cursors reference handles, so using a
  released version fails loudly with `VersionReleasedError`.
- `src/diff.ts` — the paginated engine: `openDiff()` + `diffPage()`.
- `src/reference.ts` — `referenceDiff()`: naive full-merge diff used as
  ground truth in tests.

## Usage

```ts
import { PMap, VersionStore, openDiff, diffPage } from './src/index.ts';

const store = new VersionStore<number, string>();
const v1 = PMap.from([[1, 'a'], [2, 'b']]);
const v2 = v1.set(2, 'B').set(3, 'c');

const left = store.commit(v1);
const right = store.commit(v2);

let cursor = openDiff<number>(left, right);
for (;;) {
  const page = diffPage(store, cursor, { pageSize: 100 });
  for (const item of page.items) console.log(item);
  cursor = page.cursor;
  if (page.done) break;
}
```

## Semantics

- **Page size limits output items, not scanned keys.** A page returns at
  most `pageSize` diff items; producing them may require scanning (or
  skipping) arbitrarily many identical keys. `scannedKeys` /
  `skippedKeys` on each page report the work done.
- **Cursor.** `{ left, right, lastKey }` — both version handles and the
  last processed key (`null` = from the start). Re-seeking after a key is
  O(log n) per side, so any page boundary is a cheap resume point.
- **Subtree skipping.** Reference-identical subtrees are always skipped
  (same object ⇒ same content). That is the common case between two
  versions of a persistent map.
- **Hash-trust model (explicit).** When hashes match but nodes are not
  reference-identical:
  - `'verified'` (default): descend and compare element-wise. A hash
    collision can never hide a difference.
  - `'trusted'`: accept equal hashes as proof of equality and skip. The
    caller asserts the hash is collision-resistant for their data; a
    collision can then silently mask differences. See the
    `hash collisions` test for a demonstration of both behaviors.
- **Cancellation.** Pass `signal` (any object with `aborted: boolean`,
  e.g. `AbortSignal`). An aborted page returns `cancelled: true` with the
  items produced so far and a cursor that resumes exactly where it
  stopped — including the empty page you get when the signal was already
  aborted.
- **Released versions.** Both versions are resolved at the start of every
  `diffPage` call; a released (or unknown) version throws
  `VersionReleasedError` before anything is emitted.

## Tests

Requires Node ≥ 23.6 (runs TypeScript directly via type stripping) or
Node ≥ 22.6 with `--experimental-strip-types`.

```sh
npm test
```

The suite cross-checks the paginated engine against the full-merge
reference over randomized version pairs, and covers empty pages, long
identical prefixes (asserting the skip actually engages), cancel/resume
at every page boundary, hash collisions under both trust modes, and
released versions.
