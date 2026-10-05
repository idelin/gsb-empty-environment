import { Ledger } from "./ledger";
import { ManualCalculator } from "./manualCalculator";
import { ResultCache } from "./resultCache";
import { VersionRegistry } from "./versionRegistry";

function tick(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

async function main(): Promise<void> {
  const registry = new VersionRegistry();
  const ledger = new Ledger();
  const calc = new ManualCalculator<Record<string, unknown>, Float64Array>();
  const cache = new ResultCache<Record<string, unknown>, Float64Array>({
    compute: calc.compute,
    maxEntries: 2,
    registry,
    ledger,
  });

  const input = new Float64Array([1, 2, 3, 4]);

  // 1. Miss, then a concurrent joiner merges into the same computation.
  const a = cache.request(input, { algo: "sum" });
  const b = cache.request(input, { algo: "sum" });
  await tick();
  calc.resolveOldest(new Float64Array([10]));
  await Promise.all([a, b]);

  // 2. Cache hit; the caller mutates the returned array (cache stays intact).
  const hit = await cache.request(input, { algo: "sum" });
  hit[0] = -1;

  // 3. Input is overwritten and re-versioned; the old task arrives late.
  const stale = cache.request(input, { algo: "sum", precision: "f64" });
  await tick();
  input[0] = 100;
  registry.bump(input);
  const fresh = cache.request(input, { algo: "sum", precision: "f64" });
  await tick();
  const [staleCall, freshCall] = calc.pendingIds;
  calc.resolve(freshCall!, new Float64Array([106]));
  await fresh;
  calc.resolve(staleCall!, new Float64Array([10]));
  await stale;

  // 4. A cancelled task never publishes.
  const controller = new AbortController();
  const cancelled = cache.request(input, { algo: "mean" }, { signal: controller.signal });
  await tick();
  controller.abort();
  await cancelled.catch(() => undefined);
  calc.resolveOldest(new Float64Array([26.5]));
  await tick();

  // 5. Quota: with maxEntries=2, a third distinct computation evicts the LRU entry.
  const third = cache.request(input, { algo: "sum" });
  await tick();
  calc.resolveOldest(new Float64Array([106]));
  await third;

  console.log("=== hit ledger ===");
  console.log(ledger.toString());
  console.log("\n=== summary ===");
  console.log(JSON.stringify(ledger.summary(), null, 2));
}

void main();
