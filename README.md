# layered-config

Multi-layer configuration compiler in pure TypeScript (zero dependencies), with
field-specific merge strategies, conditional layers, cross-field references,
full per-field provenance, incremental recompilation, and atomic snapshots.

Runs on Node.js >= 22.6 using native type-stripping and the built-in `node:test`
runner. `scripts/node-run.sh` auto-selects a suitable Node (prefers the newest
nvm install, falls back to `PATH`).

```sh
npm test        # run the node:test suite
npm run replay  # run the replayable end-to-end scenario
```

## Model

- **Layers** — `{ id, priority, data, enabled?, when? }`. Higher priority wins;
  ties break by insertion order. `data` is a JSON-compatible tree (object key
  order is insignificant, array order is significant).
- **Conditional enablement** — `when: { path: "flags.beta", equals: true }` is
  evaluated against the store context (`setContext`). Toggling a layer (manually
  or via context) re-resolves all affected references; nothing stale survives.
- **Commits are transactional** — `store.commit(ops[])` applies a batch of ops
  (upsert/remove/setLayerData/setEnabled/swapPriority/setContext). The batch is
  fully compiled and validated first; any error rejects the whole batch and the
  previous configuration keeps serving.

## Merge strategies (per field, by path pattern)

| strategy   | pattern example   | semantics |
|------------|-------------------|-----------|
| `deep`     | default           | objects merge key-by-key recursively; scalars/arrays replace |
| `override` | `/features/*`     | the higher layer replaces the whole field |
| `byId`     | `/services`       | arrays of `{id, ...}` merge as an ordered set-union by `id`; same-id items deep-merge |

- **Delete sentinel** `{ "$delete": true }` removes the field (or the byId item
  with `{ "id": x, "$delete": true }`). It is *not* `null`: `null` is a real
  value that gets merged in.
- **byId type conflict** — two items with the same `id` declaring different
  `type` values reject the whole batch (`TYPE_CONFLICT` with path + details).
- Most specific pattern wins; `*` matches one segment, `**` the rest.

## Cross-field references

A value that is exactly `{ "$ref": "#/some/path" }` is a *pure* reference to
another field. The compiler builds the field-level dependency graph, detects
cycles (`REF_CYCLE` carries the full cycle as a chain of paths), and reports
dangling targets (`REF_NOT_FOUND` at the referencing path). Refs are atomic:
they are never deep-merged into.

## Provenance (explain index)

Every field carries its full source chain as **structured entries** (never
formatted strings):

```json
["/server/port", [
  { "kind": "layer", "layer": "base", "priority": 0, "op": "set", "path": ["server","port"], "value": 8080 },
  { "kind": "layer", "layer": "env",  "priority": 10, "op": "set", "path": ["server","port"], "value": 9090 }
]]
["/meta/primary", [
  { "kind": "layer", "layer": "base", "priority": 0, "op": "set", "path": ["meta","primary"], "value": { "$ref": "#/server/host" } },
  { "kind": "ref", "target": ["server","host"], "resolvedFrom": ["meta","primary"] }
]]
```

## Snapshots, atomicity, subscribers

- Publishing swaps one immutable (deep-frozen) `Snapshot` holding **config +
  explain index together** — readers can never observe a torn pair.
- `subscribe(fn)` is notified synchronously after each publish with the exact
  snapshot object the store now serves.
- `getSnapshot(version)` returns historical snapshots; `rollback()` restores
  the layer state from before the most recent commit and recompiles.
- Custom `Validator`s run before every publish; a throwing validator rejects
  the commit and the old config keeps serving.

## Incremental compilation

Hot-updating one layer re-merges only the dirty subtrees (structural diff of
the layer's old/new data) and re-resolves only the refs whose location or
target touches them (transitive closure over the ref graph). The invariant —
verified by unit tests, the replay sample, and a 400-step fuzz test — is that
the incremental result (config **and** explain index) always equals a
from-scratch compile.

## Errors

All errors are `ConfigError`s with a machine-readable `code`, a structured
`path` (array of segments), optional `layer` and `details`.

## Layout

- `src/json.ts` — JSON tree, paths, equality, immutable updates, diffing
- `src/markers.ts` — delete sentinel / pure-ref markers
- `src/strategy.ts` — strategy pattern matching
- `src/merge.ts` — merge engine + provenance (`deep`/`override`/`byId`)
- `src/refs.ts` — ref graph, cycle detection, resolution
- `src/compile.ts` — full + incremental compilers
- `src/store.ts` — transactional commits, atomic publish, snapshots, rollback
- `examples/replay.ts` — replayable scenario (ops as data), self-verifying
- `test/` — `node:test` suites (merge, refs, store, replay, fuzz)
