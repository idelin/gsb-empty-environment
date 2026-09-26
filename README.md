# layered-config

Multi-layer configuration engine in TypeScript with field-specific merge
strategies, pure cross-field references, per-field provenance, incremental
hot updates and atomic, replayable snapshots. No runtime dependencies; runs
on Node.js >= 23.6 (native type stripping) and is verified with the built-in
`node:test` runner.

## Layout

- `src/json.ts` — JSON tree model, paths/JSON-Pointers, immutable updates
- `src/sentinel.ts` — the `DELETE` sentinel (distinct from `null`)
- `src/strategy.ts` — `override` / `deep` / `byId` strategy rules
- `src/merge.ts` — the merge engine; emits structured provenance records
- `src/refs.ts` — `$ref` detection, dependency graph, resolver, cycle detection
- `src/compiler.ts` — full compile + incremental single-layer hot update
- `src/store.ts` — layered store: atomic publish, history, subscribe, rollback
- `test/` — `node:test` suites; `examples/replay.ts` — replayable scenario

## Semantics

- **Layers** sit in a priority list (index 0 = lowest). A layer may carry
  `enabledWhen(context)`; flipping the context recompiles from scratch so no
  stale reference survives a layer toggle.
- **Strategies** bind to exact field paths (`*` matches one segment):
  - `deep` (default): objects merge key-by-key; scalars/arrays are won by the
    highest layer.
  - `override`: the whole field is taken from the highest layer.
  - `byId(idKey)`: arrays of items union by id; same-id objects deep-merge,
    item order is first appearance (low → high). `{id, $delete: true}` removes
    one item; `DELETE` on the field clears it. Two items with the same id but
    conflicting types reject the whole commit.
- **`DELETE` sentinel** (`{$delete: true}`) removes a key/subtree and masks
  lower layers; `null` is an ordinary value. A higher concrete value
  re-introduces a deleted key.
- **References** are pure: `{ $ref: "#/a/b" }` is replaced by the referenced
  value (chains allowed). The compiler builds the dependency graph
  (`snapshot.dependencies`), detects cycles (reported as a pointer chain) and
  rejects references to missing paths — all located to the exact field path.
- **Provenance**: every field has an explain record with the ordered
  contribution chain (layer id + path + kind + value snapshot), the winning
  layer, and `resolvedFrom` links for references — structured data, never
  concatenated strings.
- **Hot update** (`updateLayerData`) diffs one layer, recomputes only the
  changed merge units plus transitively dependent references, and republishes.
  Untouched subtrees keep reference identity; tests assert the result and the
  explain index are identical to a from-scratch compile.
- **Atomicity**: config + explain index + dependency graph are one immutable
  snapshot object swapped in a single assignment. Validation/compile errors
  happen before the swap, so a failed commit leaves the old configuration
  fully usable. Subscribers are notified after the swap and always read a
  consistent snapshot; history is append-only and `rollback(version)`
  republishes an earlier layer set as a new version.

## Commands

```sh
npm test        # node --test "test/*.test.ts"
npm run replay  # node examples/replay.ts
```

## Test results (latest run)

`npm test` — 33 tests, 33 pass, 0 fail (~0.25s):

- `test/merge.test.ts` (9): deep/override/byId semantics, DELETE vs null,
  provenance chain, same-id type conflict rejects the batch, key/array order
- `test/refs.test.ts` (9): cross-field refs, chains, missing/malformed refs,
  cycle detection, layer toggles rewiring dependencies, failed compile safety
- `test/store.test.ts` (7): atomic publish + subscribers, frozen history,
  concurrent readers, conditional layers, priority swap, rollback, unsubscribe
- `test/consistency.test.ts` (8): every operation cross-checked against a
  from-scratch merge; hot-update minimality (recomputed set + structural
  sharing); atomic rejection of broken refs / cycles / type conflicts

`npm run replay` — 12-step self-verifying scenario ending in
`REPLAY OK — 12 steps, final version v11`.
