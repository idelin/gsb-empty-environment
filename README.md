# token-move-diff

Token-level diff with **block-move detection**: given two versions of a token
sequence, it finds non-overlapping moved blocks and distinguishes them from
deletions and (re)insertions. Every token in the output carries a stable
provenance label.

Requires Node.js >= 22.6 (uses built-in TypeScript type stripping, no
dependencies, no build step).

```sh
npm test                 # run the test suite
npm run cli -- old.txt new.txt [lines|words]
```

## Pipeline

1. **Stable labels** — old token `i` is labelled `o#i`. After the block
   selection below, each new token either inherits the label `o#k` of the old
   token it is aligned to, or gets `n#j` marking it as an insertion.
2. **Candidate blocks** (`src/candidates.ts`) — all common blocks
   `(oldStart, newStart, length)` with identical token content, including
   nested and overlapping candidates caused by repeated tokens. If the full
   set exceeds `maxCandidates` (default 25 000), it falls back to maximal
   blocks plus all singleton matches, which provably keeps the same optimal
   coverage (any non-maximal block can be decomposed into singletons).
3. **Selection** (`src/select.ts`) — branch-and-bound picks a set of blocks
   that overlap neither in old nor in new positions, optimizing a
   lexicographic objective:
   1. maximize the number of preserved tokens;
   2. minimize the number of blocks;
   3. tie-break by source position (block lists compared element-wise by
      `(oldStart, newStart, length)`, earliest source anchor wins).
4. **Classification** (`src/diff.ts`) — among the selected blocks, a
   maximum-weight (by token count) order-preserving subsequence is reported
   as `keep`; the remaining blocks are `move`s. Uncovered old runs are
   deletions, uncovered new runs are insertions.

## Budget contract

`maxStates` (default 200 000) caps the branch-and-bound search. When the
budget is exhausted, the result still satisfies every structural invariant —
`optimal: false` simply means optimality was not proven. A greedy seed
(longest compatible blocks first) guarantees a decent fallback even with
`maxStates: 0`. `verifyDiff(old, new, result)` (`src/verify.ts`)
independently checks exact-match blocks, non-overlap, label consistency and
delete/insert coverage, so any returned solution is verifiable.

## Tests

- `test/diff.test.ts` — keeps, inserts, deletes, simple moves, empty inputs.
- `test/repeated.test.ts` — repeated blocks get deterministic provenance
  labels; duplicate content resolves to the earliest source position.
- `test/modification.test.ts` — a moved block with a small internal edit is
  preserved around the edit (only the edited token is delete+insert).
- `test/nested.test.ts` — nested candidates: larger block wins on coverage,
  fewer blocks win ties, source position decides full ties.
- `test/budget.test.ts` — budget exhaustion returns a verifiable, flagged
  solution; zero budget falls back to the greedy seed.
- `test/optimality.test.ts` — small-scale exhaustive cross-checks: random
  inputs vs. subset enumeration (`src/exhaustive.ts`), and all tiny inputs
  (alphabet 2, length <= 4, both candidate modes) vs. a bitmask-DP brute
  force over *all* common blocks.

## API

```ts
import { diffTokens } from "./src/diff.ts";
import { verifyDiff } from "./src/verify.ts";

const result = diffTokens(oldTokens, newTokens, {
  minBlockLength: 1,   // minimum candidate block length
  maxStates: 200_000,  // search budget
  maxCandidates: 25_000, // candidate-set cap before the reduced fallback
});
// result.blocks     -> [{ oldStart, newStart, length, kind: "keep" | "move", tokens }]
// result.deletions  -> maximal uncovered old runs
// result.insertions -> maximal uncovered new runs
// result.oldLabels / result.newLabels -> stable provenance labels
// result.optimal    -> false when the budget ran out
```
