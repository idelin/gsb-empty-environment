import {
  MONO_V1,
  SERIF_V1,
  SERIF_V2,
  STYLE_A,
  STYLE_A_SPACED,
  makeNodeTree,
} from "../src/fixtures.ts";
import { LayoutEngine } from "../src/layout.ts";
import { FontMeasurer, FontRegistry } from "../src/measurer.ts";
import { ControllableScheduler } from "../src/scheduler.ts";
import type { LayoutBox, LayoutResult } from "../src/types.ts";

function printTree(box: LayoutBox, indent = ""): void {
  console.log(
    `${indent}${box.id}: ${box.width.toFixed(1)}x${box.height.toFixed(1)} lines=${box.lines}`,
  );
  for (const child of box.children) printTree(child, `${indent}  `);
}

async function drive<T>(scheduler: ControllableScheduler, promise: Promise<T>): Promise<T> {
  let settled = false;
  let value: T;
  promise.then((v) => {
    value = v;
    settled = true;
  });
  while (!settled) {
    await scheduler.pumpAll();
    await new Promise((resolve) => setImmediate(resolve));
  }
  return value!;
}

function show(result: LayoutResult): void {
  console.log(`\n[${result.channel}] round=${result.round}`);
  printTree(result.tree);
}

const registry = new FontRegistry();
registry.register(SERIF_V1);
registry.register(MONO_V1);
const scheduler = new ControllableScheduler();
const measurer = new FontMeasurer(registry, scheduler);
const engine = new LayoutEngine(measurer);

console.log("=== 1. Estimate and final layout are published separately ===");
const session = engine.layout(makeNodeTree());
show(session.estimate);
const final = await drive(scheduler, session.final);
show(final);

console.log("\n=== 2. Same-key requests merge into one shared task ===");
const h1 = measurer.measure("abcd", STYLE_A);
const h2 = measurer.measure("abcd", STYLE_A);
h1.cancel(); // cancelling one subscriber keeps the other waiter alive
await scheduler.pumpAll();
console.log(`tasks started: ${measurer.stats.tasksStarted}, merged: ${measurer.stats.subscribersMerged}`);
console.log(`surviving subscriber got width=${(await h2.promise).width}`);

console.log("\n=== 3. Same text, different letter-spacing: no shared results ===");
const tight = measurer.measure("ijkl", STYLE_A);
const spaced = measurer.measure("ijkl", STYLE_A_SPACED);
await scheduler.pumpAll();
console.log(`ls=0 -> ${(await tight.promise).width}px, ls=2 -> ${(await spaced.promise).width}px`);

console.log("\n=== 4. Byte-budget accounting (actual entry sizes) ===");
for (const [name, cache] of [
  ["font", measurer.fontCache],
  ["cluster", measurer.clusterCache],
  ["measure", measurer.measureCache],
] as const) {
  console.log(
    `${name} cache: ${cache.size} entries, ${cache.usedBytes}/${cache.budgetBytes} bytes, conserved=${cache.checkConservation()}`,
  );
}

console.log("\n=== 5. Font hot update mid-flight: stale reply discarded, atomic switch ===");
// Fresh instances, so the v1 font load is genuinely still in flight.
const registry2 = new FontRegistry();
registry2.register(SERIF_V1);
registry2.register(MONO_V1);
const scheduler2 = new ControllableScheduler();
const measurer2 = new FontMeasurer(registry2, scheduler2);
const pending = measurer2.measure("abcd efgh", STYLE_A);
await scheduler2.pump(1); // measure job suspends: v1 font reply still queued
registry2.hotReload(SERIF_V2); // hot update lands before the reply arrives
console.log(`cache generation after hot reload: ${measurer2.measureCache.currentGeneration}`);
await scheduler2.pumpAll();
console.log(`stale replies discarded: ${measurer2.stats.staleDiscards}`);
console.log(`"abcd efgh" measures ${(await pending.promise).width}px with Serif v2 (was 68px on v1)`);

console.log("\n=== 6. Re-layout after hot update uses the new generation ===");
const engine2 = new LayoutEngine(measurer2);
const session2 = engine2.layout(makeNodeTree());
show(session2.estimate);
show(await drive(scheduler2, session2.final));
