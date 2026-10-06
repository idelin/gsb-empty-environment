# history

Typed navigation-history library: `push` / `replace` / `back` / `forward`,
revision-bound async restore, and capacity trimming.

## Semantics

- `push(value)` truncates any forward branch (entries after the cursor),
  appends a new entry with a fresh `id` and `revision: 1`, then trims the
  oldest entries if the stack exceeds `capacity`.
- `replace(value)` rewrites the current entry in place: the `id` is kept,
  `revision` is incremented. On an empty history it behaves like `push`.
- `back()` / `forward()` move the cursor and return the entry landed on, or
  `undefined` when already at the oldest / newest entry.
- `restore(loader)` snapshots the current entry `(id, revision)`, awaits
  `loader(entry)`, and applies the result only if an entry with that `id`
  still exists and its `revision` is unchanged. Stale responses (entry was
  replaced, trimmed, or its branch discarded) are dropped and the promise
  resolves `false`. A successful apply writes the value and bumps `revision`,
  so of two concurrent restores the first to resolve wins.

## Layout

- `src/history.ts` — TypeScript source of truth.
- `dist/history.js` / `dist/history.d.ts` — compiled output (`npm run build`
  with `typescript` installed regenerates them).
- `test/history.test.mjs` — deterministic tests plus a randomized
  differential test (40 seeds x 400 ops) that checks every step against an
  array reference model, covering consecutive backs, stale restore responses,
  and capacity trimming.

## Test

```sh
npm test
```
