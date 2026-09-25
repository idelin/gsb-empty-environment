import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  MONO_V1,
  SERIF_V1,
  SERIF_V2,
  STYLE_A,
  STYLE_A_SPACED,
} from "../src/fixtures.ts";
import { FontMeasurer, FontRegistry } from "../src/measurer.ts";
import { ControllableScheduler } from "../src/scheduler.ts";

function setup() {
  const registry = new FontRegistry();
  registry.register(SERIF_V1);
  registry.register(MONO_V1);
  const scheduler = new ControllableScheduler();
  const measurer = new FontMeasurer(registry, scheduler);
  return { registry, scheduler, measurer };
}

describe("FontMeasurer", () => {
  it("merges same-key requests into one shared task", async () => {
    const { scheduler, measurer } = setup();
    const first = measurer.measure("abcd", STYLE_A);
    const second = measurer.measure("abcd", STYLE_A);
    assert.equal(measurer.stats.tasksStarted, 1);
    assert.equal(measurer.stats.subscribersMerged, 1);
    await scheduler.pumpAll();
    const [a, b] = await Promise.all([first.promise, second.promise]);
    assert.equal(a.width, 32); // 4 glyphs * 500 units * 16/1000
    assert.equal(a, b);
  });

  it("cancelling one subscriber keeps the other waiters alive", async () => {
    const { scheduler, measurer } = setup();
    const first = measurer.measure("abcd", STYLE_A);
    const second = measurer.measure("abcd", STYLE_A);
    first.cancel();
    await scheduler.pumpAll();
    const measurement = await second.promise;
    assert.equal(measurement.width, 32, "shared task completes for remaining waiter");
  });

  it("cancels the shared task only when its last subscriber leaves", async () => {
    const { scheduler, measurer } = setup();
    const only = measurer.measure("abcd", STYLE_A);
    only.cancel();
    await scheduler.pumpAll();
    await assert.rejects(only.promise, /cancelled/);
    assert.equal(measurer.peekFinal("abcd", STYLE_A), undefined, "nothing is cached");
  });

  it("serves repeat requests from the cache", async () => {
    const { scheduler, measurer } = setup();
    const first = measurer.measure("abcd", STYLE_A);
    await scheduler.pumpAll();
    await first.promise;
    const second = measurer.measure("abcd", STYLE_A);
    assert.equal(measurer.stats.cacheHits, 1);
    assert.equal(measurer.stats.tasksStarted, 1);
    assert.equal((await second.promise).width, 32);
  });

  it("never lets a stale async reply overwrite a newer font version", async () => {
    const { registry, scheduler, measurer } = setup();
    const handle = measurer.measure("abcd", STYLE_A);
    // The measure job starts and suspends waiting for the v1 font reply.
    await scheduler.pump(1);
    // Hot update lands while the v1 reply is still in flight: the whole cache
    // generation is swapped atomically.
    registry.hotReload(SERIF_V2);
    await scheduler.pumpAll();
    const measurement = await handle.promise;
    assert.equal(measurement.width, 38.4, "result is computed from Serif v2");
    assert.ok(measurer.stats.staleDiscards >= 1, "stale v1 reply was discarded");
    assert.equal(measurer.peekFinal("abcd", STYLE_A)?.width, 38.4);
    assert.equal(measurer.measureCache.size, 1, "no stale v1 entry survives");
  });

  it("validates the version of a late-arriving font load", async () => {
    const { registry, scheduler, measurer } = setup();
    const handle = measurer.measure("abcd", STYLE_A);
    await scheduler.pump(1); // measure job enqueues load-font@Serif@v1
    registry.hotReload(SERIF_V2); // registry moves to v2 before the reply lands
    await scheduler.pumpAll();
    await handle.promise;
    const cachedFace = measurer.fontCache.peek("Serif");
    assert.equal(cachedFace?.version, 2, "stale v1 face is never cached");
    assert.equal(measurer.fontCache.currentGeneration, 1, "generation switched once");
  });

  it("does not share results between different letter-spacing of the same text", async () => {
    const { scheduler, measurer } = setup();
    const tight = measurer.measure("abcd", STYLE_A);
    const spaced = measurer.measure("abcd", STYLE_A_SPACED);
    assert.equal(measurer.stats.tasksStarted, 2, "distinct keys, distinct tasks");
    await scheduler.pumpAll();
    assert.equal((await tight.promise).width, 32);
    assert.equal((await spaced.promise).width, 40); // +2px tracking per glyph
    assert.equal(measurer.peekFinal("abcd", STYLE_A)?.width, 32);
    assert.equal(measurer.peekFinal("abcd", STYLE_A_SPACED)?.width, 40);
  });

  it("publishes estimate and final on separate channels", async () => {
    const { scheduler, measurer } = setup();
    const events: Array<[string, number]> = [];
    measurer.onEstimate((_key, m) => events.push(["estimate", m.width]));
    measurer.onFinal((_key, m) => events.push(["final", m.width]));
    const request = measurer.request("abcd", STYLE_A);
    assert.deepEqual(events, [["estimate", 38.4]], "estimate is synchronous");
    await scheduler.pumpAll();
    const final = await request.final;
    assert.equal(final.width, 32);
    assert.deepEqual(events, [
      ["estimate", 38.4],
      ["final", 32],
    ]);
  });
});
