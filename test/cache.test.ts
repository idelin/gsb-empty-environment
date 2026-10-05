import { Ledger, ManualCalculator, ResultCache, VersionRegistry } from "../src";
import {
  assert,
  assertDeepEqual,
  assertEqual,
  expectReject,
  test,
  tick,
} from "./tinytest";

type Params = Record<string, unknown>;

function makeCache(maxEntries = 8) {
  const registry = new VersionRegistry();
  const ledger = new Ledger();
  const calc = new ManualCalculator<Params, Float64Array>();
  const cache = new ResultCache<Params, Float64Array>({
    compute: calc.compute,
    maxEntries,
    registry,
    ledger,
  });
  return { registry, ledger, calc, cache };
}

test("miss -> publish -> hit, and hit returns an equal array", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([1, 2, 3]);

  const first = cache.request(input, { algo: "sum" });
  await tick();
  assertEqual(calc.calls.length, 1, "exactly one computation starts");
  calc.resolveOldest(new Float64Array([6]));
  assertDeepEqual([...await first], [6], "first caller gets the computed value");

  const second = await cache.request(input, { algo: "sum" });
  assertDeepEqual([...second], [6], "second caller hits the cache");
  assertEqual(calc.calls.length, 1, "no recomputation on hit");

  assertDeepEqual(
    ledger.kinds(),
    ["miss", "compute-start", "publish", "hit"],
    "ledger records the full miss/publish/hit script",
  );
});

test("caller mutating the returned array cannot corrupt the cache", async () => {
  const { calc, cache } = makeCache();
  const input = new Float64Array([1, 2, 3]);

  const first = cache.request(input, {});
  await tick();
  calc.resolveOldest(new Float64Array([42]));
  const result = await first;
  result[0] = -999;

  const again = await cache.request(input, {});
  assertDeepEqual([...again], [42], "cached copy is immune to caller mutation");
  assertEqual(calc.calls.length, 1, "still no recomputation");
});

test("concurrent same-key requests merge into one computation", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([5, 5]);

  const a = cache.request(input, { algo: "sum" });
  const b = cache.request(input, { algo: "sum" });
  await tick();
  assertEqual(calc.calls.length, 1, "two requests, one computation");
  calc.resolveOldest(new Float64Array([10]));
  assertDeepEqual([...await a], [10], "first joiner gets the value");
  assertDeepEqual([...await b], [10], "second joiner gets the value");

  const kinds = ledger.kinds();
  assert(kinds[0] === "miss" && kinds[1] === "compute-start", "first request misses");
  assertEqual(kinds[2], "join", "second request joins the in-flight computation");
  assertEqual(ledger.count("publish"), 1, "result published once");
});

test("joined callers each receive an independent copy", async () => {
  const { calc, cache } = makeCache();
  const input = new Float64Array([1]);

  const a = cache.request(input, {});
  const b = cache.request(input, {});
  await tick();
  calc.resolveOldest(new Float64Array([7]));
  const ra = await a;
  const rb = await b;
  ra[0] = 0;
  assertDeepEqual([...rb], [7], "mutation by one joiner does not leak to the other");
});

test("param changes never false-hit; key order is irrelevant", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([1]);

  const p1 = cache.request(input, { algo: "fft", size: 8 });
  await tick();
  calc.resolveOldest(new Float64Array([1]));
  await p1;

  await cache.request(input, { size: 8, algo: "fft" });
  assertEqual(ledger.count("hit"), 1, "reordered keys still hit");

  const p2 = cache.request(input, { algo: "fft", size: 16 });
  await tick();
  calc.resolveOldest(new Float64Array([2]));
  await p2;
  assertEqual(ledger.count("miss"), 2, "changed size misses");

  const p3 = cache.request(input, { algo: "fft", size: "8" });
  await tick();
  calc.resolveOldest(new Float64Array([3]));
  await p3;
  assertEqual(ledger.count("miss"), 3, "string \"8\" does not collide with number 8");

  assertEqual(calc.calls.length, 3, "three distinct computations ran");
});

test("views over one buffer share the real underlying version", async () => {
  const { registry, ledger, calc, cache } = makeCache();
  const buffer = new Float64Array([1, 2, 3, 4]);
  const left = buffer.subarray(0, 2);
  const right = buffer.subarray(2, 4);

  const p1 = cache.request(left, {});
  await tick();
  calc.resolveOldest(new Float64Array([3]));
  await p1;

  await cache.request(new Float64Array(buffer.buffer).subarray(0, 2), {});
  assertEqual(ledger.count("hit"), 1, "equivalent view over same buffer hits");

  registry.bump(right);
  const p2 = cache.request(left, {});
  await tick();
  calc.resolveOldest(new Float64Array([3]));
  await p2;
  assertEqual(ledger.count("miss"), 2, "bump via a sibling view invalidates left");

  const p3 = cache.request(right, {});
  await tick();
  calc.resolveOldest(new Float64Array([7]));
  await p3;
  assertEqual(ledger.count("miss"), 3, "different view window is a different key");
});

test("late old task is discarded as stale, newer result stays", async () => {
  const { registry, ledger, calc, cache } = makeCache();
  const input = new Float64Array([1, 2]);

  const staleRequest = cache.request(input, { algo: "sum" });
  await tick();
  assertEqual(calc.calls.length, 1, "v0 computation running");

  input[0] = 100;
  registry.bump(input);

  const freshRequest = cache.request(input, { algo: "sum" });
  await tick();
  assertEqual(calc.calls.length, 2, "v1 computation running");

  calc.resolve(2, new Float64Array([102]));
  assertDeepEqual([...await freshRequest], [102], "fresh caller gets fresh value");

  calc.resolve(1, new Float64Array([3]));
  assertDeepEqual([...await staleRequest], [3], "stale caller still gets its correct result");
  await tick();

  assertEqual(ledger.count("stale-discard"), 1, "late v0 result discarded as stale");
  assertEqual(ledger.count("publish"), 1, "only the fresh result was published");
  assertEqual(cache.size, 1, "cache holds exactly the fresh entry");

  const again = await cache.request(input, { algo: "sum" });
  assertDeepEqual([...again], [102], "subsequent request hits the fresh entry");
});

test("in-flight computation sees a snapshot, not later overwrites", async () => {
  const { registry, calc, cache } = makeCache();
  const input = new Float64Array([1, 2, 3]);

  const pending = cache.request(input, {});
  await tick();
  input[0] = 99;
  registry.bump(input);

  const seenByCompute = calc.calls[0]!.input as Float64Array;
  assertDeepEqual([...seenByCompute], [1, 2, 3], "compute received pre-mutation snapshot");

  calc.resolveOldest(new Float64Array([6]));
  await pending;
});

test("cancelled task never publishes a half-finished result", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([1, 2]);
  const controller = new AbortController();

  const pending = cache.request(input, {}, { signal: controller.signal });
  await tick();
  controller.abort();
  const error = await expectReject(pending, "cancelled caller rejects");
  assertEqual(error.name, "AbortError", "rejection is an AbortError");
  assert(calc.calls[0]!.signal.aborted, "abort propagates to the computation");

  calc.resolveOldest(new Float64Array([3]));
  await tick();
  assertEqual(ledger.count("cancel-drop"), 1, "late result after cancel is dropped");
  assertEqual(ledger.count("publish"), 0, "nothing was published");
  assertEqual(cache.size, 0, "cache stays empty");

  const retry = cache.request(input, {});
  await tick();
  assertEqual(calc.calls.length, 2, "retry after cancel recomputes");
  calc.resolveOldest(new Float64Array([3]));
  assertDeepEqual([...await retry], [3], "retry succeeds");
});

test("one joiner cancelling does not cancel the shared computation", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([4]);
  const controller = new AbortController();

  const a = cache.request(input, {}, { signal: controller.signal });
  const b = cache.request(input, {});
  await tick();
  controller.abort();
  await expectReject(a, "cancelled joiner rejects");
  assert(!calc.calls[0]!.signal.aborted, "computation keeps running for the other joiner");

  calc.resolveOldest(new Float64Array([8]));
  assertDeepEqual([...await b], [8], "remaining joiner gets the result");
  assertEqual(ledger.count("publish"), 1, "result still published");
});

test("quota reclamation evicts least-recently-used entries", async () => {
  const { ledger, calc, cache } = makeCache(2);
  const input = new Float64Array([1]);

  async function fill(params: Params, value: number): Promise<void> {
    const pending = cache.request(input, params);
    await tick();
    calc.resolveOldest(new Float64Array([value]));
    await pending;
  }

  await fill({ n: 1 }, 1);
  await fill({ n: 2 }, 2);
  await cache.request(input, { n: 1 });
  await fill({ n: 3 }, 3);

  assertEqual(cache.size, 2, "quota enforced");
  assertEqual(ledger.count("evict"), 1, "exactly one eviction");
  const evicted = ledger.entries().find((entry) => entry.kind === "evict");
  assert(evicted !== undefined && evicted.key.includes('"n"=N:2'), "LRU victim is n=2, not the recently-hit n=1");

  await cache.request(input, { n: 1 });
  assertEqual(ledger.count("hit"), 2, "recently-used entry survived");
  const evictedRetry = cache.request(input, { n: 2 });
  await tick();
  calc.resolveOldest(new Float64Array([2]));
  await evictedRetry;
  assertEqual(ledger.count("miss"), 4, "evicted entry recomputes");
});

test("compute failure rejects subscribers and is not cached", async () => {
  const { ledger, calc, cache } = makeCache();
  const input = new Float64Array([1]);

  const boom = cache.request(input, {});
  await tick();
  calc.rejectOldest?.(new Error("nope"));
  await expectReject(boom, "failure propagates to the caller");
  assertEqual(ledger.count("error"), 1, "failure recorded in the ledger");
  assertEqual(cache.size, 0, "failures are never cached");

  const retry = cache.request(input, {});
  await tick();
  assertEqual(calc.calls.length, 2, "retry recomputes after failure");
  calc.resolveOldest(new Float64Array([9]));
  assertDeepEqual([...await retry], [9], "retry succeeds");
});
