# diagnostic-relocation

Version-aware relocation of diagnostic records across consecutive document
edits. A diagnostic is bound to a **range in a specific version** plus a
**context summary** (prefix / suffix / target text). After any number of
later edits, `relocate` returns exactly one of three outcomes:

| status      | meaning                                                                 |
| ----------- | ----------------------------------------------------------------------- |
| `certain`   | a single position, with its basis (`identity` or `text-similarity`)      |
| `ambiguous` | several candidates with generation evidence — never a silent first pick |
| `invalid`   | the target is gone (or the endpoint version was reclaimed)               |

Requires Node.js >= 23.6 (runs TypeScript directly via type stripping;
only erasable syntax is used). No dependencies.

```sh
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## Model

- **Offsets are UTF-16 code units** everywhere. Edit boundaries that would
  split a surrogate pair are rejected; context capture snaps outward so it
  never cuts a pair (`src/utf16.ts`).
- **Identity tracking** (`src/history.ts`): each diagnostic owns a marker
  with a stable id. Every commit transforms all markers through the
  commit's edits and accumulates per-step evidence (`shift`, `expand`,
  `shrink`, `move-follow`, `deleted`). Markers are snapshotted per version,
  so undo branches and merges keep independent, composable mappings.
- **Deletions are tombstones.** A fully deleted marker stays `deleted`
  forever: identical text appearing elsewhere never revives the diagnostic.
- **Moves need a stable id.** `move` with `moveId` transports enclosed
  markers precisely; without one it degrades to delete + insert semantics
  (target dies, no revival at the destination).
- **Text similarity is a fallback, never a disguise** (`src/similarity.ts`).
  When identity evidence is unavailable (marker unknown on that branch,
  bind version reclaimed), the context summary is searched
  line-ending-insensitively (LF and CRLF). Every occurrence becomes a
  candidate with generation evidence; worse-context occurrences are
  excluded with reasons. A unique best candidate is `certain` with
  `basis: 'text-similarity'`; a tie is `ambiguous`. Duplicates are never
  resolved by picking the first match.
- **Composable position mapping**: `history.mapRange(range, from, to)`
  walks the version path (down to the LCA with inverse edits, then up)
  and composes each step's transform (`src/edits.ts`). `src/reference.ts`
  is an independent step-by-step mapper; the test suite cross-checks the
  composed results against it for duplicate text, whole-block moves and
  branch merges.
- **History reclamation**: `reclaim()` collects versions that are not
  head/pinned or their ancestors. Diagnostics whose lineage was reclaimed
  degrade to the similarity fallback, with the reclamation recorded in the
  evidence.

## API sketch

```ts
import { History } from './src/index.ts';

const h = new History('hello world foo');
const diag = h.bind({ range: { start: 6, end: 11 }, message: 'world' });
h.commit([{ kind: 'insert', at: 0, text: 'say ' }]);
h.relocate(diag);
// { status: 'certain', basis: 'identity', range: { start: 10, end: 15 }, ... }
```

Edits: `insert` / `delete` / `replace` / `move` (`move.to` is the insertion
offset after the block is removed; `moveId` enables precise following).
History: `commit`, `undo`, `checkout`, `merge` (rebase-style replay of the
other branch's edits), `pin` / `unpin`, `reclaim`.

## Test coverage (26 tests, node:test)

- `test/utf16.test.ts` — UTF-16 offsets, surrogate-pair protection.
- `test/identity.test.ts` — precise shifts, composed multi-version mapping,
  tombstones, stable-id moves, partial deletion, CRLF.
- `test/similarity.test.ts` — fallback certainty, duplicate ambiguity,
  exclusion evidence, missing target, CRLF/LF normalization.
- `test/branches.test.ts` — undo branches, reclamation, merge replay.
- `test/reference.test.ts` — differential checks against the independent
  step-by-step reference mapper (duplicates, block moves, merges,
  backward composition).
