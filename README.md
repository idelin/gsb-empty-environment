# tree-diff

An optimizing differ for **ordered syntax trees** (TypeScript, zero runtime
dependencies). Nodes carry a `type`, an optional `value`, and an optional
**stable id**. The differ produces a complete, replayable edit script of
`update` / `insert` / `delete` / `move` ops, minimizing total edit cost.

```ts
import { diff, applyPatch, verifyPatch } from './src/index.ts';

const result = diff(sourceTree, targetTree, { budget: 4000 });
// result.patch    — ordered ops; paths refer to the evolving tree
// result.cost     — weighted cost under the cost model
// result.optimal  — false when the budget interrupted the proof of optimality
applyPatch(sourceTree, result.patch);        // independent applier
verifyPatch(sourceTree, result.patch, targetTree); // => true
```

Requires Node >= 23.6 (runs TypeScript directly via type stripping) or any
recent `tsc` (`npm run typecheck`). Tests: `npm test`.

## How it works

1. **Forced id matching.** Nodes whose stable id occurs on both sides are
   bound together (k-th occurrence to k-th); the roots are always paired.
2. **Greedy seed.** Exact-subtree hashing plus a top-down pass yields a
   complete matching — the guaranteed-usable fallback patch.
3. **Branch-and-bound.** Target nodes are decided in BFS order (bind to an
   unused pairable source node, or insert). `f = g + h` is admissible, so a
   exhausted queue proves optimality; leaves are scored by generating the
   real script. When `budget` expansions run out, the best complete patch
   found so far is returned with `optimal: false`.
4. **Script generation** (Chawathe-style). BFS over the target: insert
   unmatched nodes, update/re-parent matched ones, align siblings via LCS,
   delete leftover source subtrees. Beforehand the matching is
   **normalized**: pairs whose realization would move a node into its own
   descendant (ancestor cycle) are removed, preferring non-forced pairs with
   the smallest subtrees.
5. **Independent applier.** `applyPatch` shares no state with the differ and
   re-validates every path; `diff` self-checks each result with
   `verifyPatch` before returning.
6. **Exhaustive checker.** `exhaustiveMinCost` / `checkDeterminism`
   enumerate *all* matchings on small trees to confirm the claimed optimum
   and to confirm the differ picks deterministically among equivalent
   (equal-cost) solutions.

## Semantics and cost model

- Default costs: update 1, insert 1/node, delete 1/node, move 1/subtree
  (flat). Override via `diff(a, b, { costs: { ... } })`.
- Two nodes are pairable only with equal `type` and compatible ids (equal,
  or both absent). A type change is delete+insert; a disappearing id is a
  deletion, not a rename.
- `move` detaches first, then resolves the destination path — see the op
  convention documented in `src/apply.ts`.
- The search is fully deterministic: fixed candidate order, `(f, depth,
  sequence)` heap order, incumbent replaced only on strictly lower cost.
  Same input ⇒ byte-identical patch, even when several optima exist.

## Limits

- Optimality is proven over the space of matchings; trees beyond ~a dozen
  nodes each may need a larger `budget` for the proof (the returned patch is
  always complete regardless).
- Trees are capped at 32767 nodes per side (Int16 search state).
