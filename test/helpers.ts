import assert from "node:assert/strict";
import { compileFromScratch, StrategyMap } from "../src/index.ts";
import type { LayeredConfigStore, Snapshot, StrategyRule } from "../src/index.ts";

/** Captures the thrown error (assert.throws does not return it). */
export function capture<T extends Error>(
  fn: () => unknown,
  ctor: new (...a: never[]) => T,
): T {
  try {
    fn();
  } catch (e) {
    assert.ok(e instanceof ctor, `expected ${ctor.name}, got ${String(e)}`);
    return e as T;
  }
  throw new Error(`expected ${ctor.name} to be thrown`);
}

/**
 * Recomputes the configuration from scratch (independent full compile of the
 * snapshot's own layer definitions and context) and asserts the store's
 * published snapshot matches it exactly: config, explain index, dependency
 * graph.
 */
export function assertMatchesScratch(
  store: LayeredConfigStore,
  rules: readonly StrategyRule[],
): Snapshot {
  const snap = store.snapshot();
  const active = snap.layers
    .filter((l) => l.enabledWhen === undefined || l.enabledWhen(snap.context))
    .map((l) => ({ id: l.id, data: l.data }));
  const scratch = compileFromScratch(active, new StrategyMap(rules));
  assert.deepEqual(snap.config, scratch.config, "config diverges from scratch compile");
  assert.deepEqual(snap.explain, scratch.explain, "explain index diverges from scratch compile");
  assert.deepEqual(snap.dependencies, scratch.dependencies, "dependency graph diverges");
  return snap;
}

/** Asserts a snapshot's explain index is consistent with its own config. */
export function assertInternallyConsistent(snap: Snapshot): void {
  for (const [pointer, rec] of Object.entries(snap.explain)) {
    assert.equal(rec.pointer, pointer, `record key mismatch at ${pointer}`);
    if (rec.resolvedFrom !== null && rec.resolvedFrom !== undefined) {
      assert.equal(
        snap.dependencies[pointer],
        rec.resolvedFrom,
        `dependency graph and explain disagree at ${pointer}`,
      );
    }
  }
}
