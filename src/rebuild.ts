import { Store, type Batch, type StoreConfig } from "./store.ts";
import { deepEqual, type JsonValue } from "./json.ts";

/**
 * Full rebuild: replay every batch from an empty store.
 * Used as the ground-truth oracle — incremental application must converge
 * to exactly the same snapshot.
 */
export function rebuild(config: StoreConfig, batches: Batch[]): Store {
  const store = new Store(config);
  for (const batch of batches) store.applyBatch(batch);
  return store;
}

export interface CompareResult {
  equal: boolean;
  incremental: JsonValue;
  rebuilt: JsonValue;
}

/** Compare an incrementally-built store against a full rebuild. */
export function compareWithRebuild(
  incremental: Store,
  config: StoreConfig,
  batches: Batch[],
): CompareResult {
  const rebuilt = rebuild(config, batches);
  const a = incremental.snapshot();
  const b = rebuilt.snapshot();
  return { equal: deepEqual(a, b), incremental: a, rebuilt: b };
}
