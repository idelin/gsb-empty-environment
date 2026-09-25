import assert from "node:assert/strict";
import { compileFull, ConfigStore, StrategyMap, type CompileResult, type Snapshot, type StrategyRule } from "../src/index.ts";

/** The invariant: any incremental/rollback result must equal a from-scratch compile. */
export function assertMatchesScratch(store: ConfigStore, rules: StrategyRule[]): CompileResult {
  const scratch = compileFull(store.getLayerStates(), store.getContext(), new StrategyMap(rules));
  const snap = store.getSnapshot();
  assert.deepStrictEqual(snap.config, scratch.config, "config must equal from-scratch compile");
  assert.deepStrictEqual(snap.explain, scratch.explain.map, "explain index must equal from-scratch compile");
  return scratch;
}

export function assertSnapshotConsistent(snap: Snapshot): void {
  assert.ok(Object.isFrozen(snap.config), "config must be frozen");
  assert.ok(Object.isFrozen(snap), "snapshot must be frozen");
  // every explain entry points at a declared layer (or is a ref entry)
  const layerIds = new Set(snap.layers.map((l) => l.id));
  for (const [path, entries] of snap.explain) {
    assert.ok(Array.isArray(entries), `entries at ${path}`);
    for (const e of entries) {
      if (e.kind === "layer") assert.ok(layerIds.has(e.layer), `entry at ${path} names known layer`);
      else assert.ok(Array.isArray(e.target), `ref entry at ${path} has structured target`);
    }
  }
}
