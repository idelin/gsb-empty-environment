# tree-view-core

Headless tree-view engine in TypeScript. No DOM, no browser layout engine —
row layout is pure arithmetic over fixture-provided glyph metrics. Runs
directly on Node.js >= 23 (native type stripping); tests use the built-in
`node:test` runner. No dependencies, no build step.

```sh
node --test tests/*.test.ts   # run the test suite
node examples/demo.ts         # run the demo session
```

## Layout

- `src/model.ts` — tree structure, incremental visible sequence, versioning,
  async child loading, reordering, filtering.
- `src/view.ts` — focus, multi-selection, range anchor, type-ahead; each
  bound to the tree version.
- `src/metrics.ts` — row layout from fixture glyph metrics.
- `src/fixtures.ts` — node-tree fixture, glyph-metrics fixture, and a
  manually resolvable `DeferredLoader` for deterministic async tests.
- `tests/` — unit tests plus a finite event-exploration test with replay.

## Visible sequence is incremental

Every node caches `vc` (visible rows in its subtree, itself included).
Expand/collapse/insert/remove/move/load-apply only touch the affected
subtree and its ancestor chain — never a whole-tree traversal.
`indexOf`/`at`/`visibleSlice` are O(depth) / O(depth + count). The single
exception is `setFilter`, which recomputes match counts once per call.
`model.stats.fullRecomputes` counts whole-tree recomputations and a test
asserts structural edits never increment it. A brute-force oracle in the
tests verifies the incremental sequence after every edit.

## Version binding

`model.version` bumps on every mutation. Focus, the range anchor, the
selection and the type-ahead buffer each record the version at which they
were validated, and re-validate lazily and independently on next read:

- focus: must exist, be visible and enabled, else a deterministic fallback
  applies (nearest enabled visible row at/after the last visible index,
  else the nearest before, else null);
- anchor: must exist, be visible and enabled, else falls back to the focus;
- selection: removed or newly-disabled ids are dropped; nodes merely hidden
  by a filter keep their selection;
- type-ahead: a stale buffer restarts against the new tree.

## Disabled-node rules

1. Disabled nodes can never be selected (plain/ctrl clicks ignored, range
   selection skips over them).
2. Keyboard navigation (up/down/home/end) and type-ahead never land on a
   disabled node.
3. Disabled nodes may still be expanded/collapsed so their children stay
   reachable.
4. A focused node that becomes disabled/hidden/removed triggers the
   deterministic focus fallback.
5. A selected node that becomes disabled or is removed drops out of the
   selection.

## Async loading

Expanding a lazy node starts a token-guarded load. A result is applied only
if the token still matches — superseded (`reload`), cancelled (`cancelLoad`)
or detached (subtree removed) loads are dropped. Applying children never
touches `expanded`, so a load that resolves after the user closed the branch
cannot re-expand it. Async-added children never reset focus or selection.

## Exploration testing

`tests/exploration.test.ts` drives the view with a seeded pseudo-random
event stream (toggle/collapse/navigation/type-ahead/selection/insert/
remove/move/filter/disable/cancel/resolve) and asserts the invariants after
every event: no disabled node selected or focused, anchor consistency,
version monotonicity, and incremental-vs-brute-force visible-sequence
equality. Replay consistency is checked two ways: the same seed regenerates
the same event log and final state, and replaying a recorded log on a fresh
instance reproduces the exact snapshot.
