import test from "node:test";
import assert from "node:assert/strict";

import { ElementTracker } from "../src/elements.ts";
import { Pipeline } from "../src/pipeline.ts";
import type { StageDef } from "../src/pipeline.ts";

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

function mapStage(name: string, capacity: number, f: (v: any) => any): StageDef {
  return {
    name,
    capacity,
    fn: (el, ctx) => [ctx.create(f(el.value))],
  };
}

function* range(start: number, count: number): Generator<number> {
  for (let i = 0; i < count; i += 1) yield start + i;
}

/** Every created element must be released exactly once. */
function expectCleanShutdown(tracker: ElementTracker): void {
  tracker.assertAllReleased();
  assert.equal(
    tracker.releases.length,
    tracker.createdCount,
    `expected ${tracker.createdCount} releases, got ${tracker.releases.length}`,
  );
  const ids = new Set(tracker.releases.map((r) => r.id));
  assert.equal(ids.size, tracker.releases.length, "an element was released twice");
}

test("three-stage chain delivers every element in order", async () => {
  const tracker = new ElementTracker();
  const pipeline = new Pipeline([
    mapStage("parse", 1, (v: number) => v + 1),
    mapStage("transform", 2, (v: number) => v * 2),
    mapStage("format", 1, (v: number) => `#${v}`),
  ]);
  const out: string[] = [];
  const result = await pipeline.run(
    range(0, 20),
    (el) => {
      out.push(el.value as string);
    },
    { tracker },
  );
  assert.deepEqual(out, Array.from({ length: 20 }, (_, i) => `#${(i + 1) * 2}`));
  assert.equal(result.produced, 20);
  assert.equal(result.consumed, 20);
  assert.equal(result.consumerStopped, false);
  expectCleanShutdown(tracker);
});

test("paused consumer progressively blocks upstream pulls", async () => {
  const tracker = new ElementTracker();
  let pulled = 0;
  function* infiniteSource(): Generator<number> {
    for (;;) {
      pulled += 1;
      yield pulled;
    }
  }
  const pipeline = new Pipeline([
    mapStage("s1", 1, (v) => v),
    mapStage("s2", 1, (v) => v),
    mapStage("s3", 1, (v) => v),
  ]);
  let openGate!: () => void;
  const gate = new Promise<void>((resolve) => {
    openGate = resolve;
  });
  let sinkCalls = 0;
  const runPromise = pipeline.run(
    infiniteSource(),
    async () => {
      sinkCalls += 1;
      if (sinkCalls === 1) await gate; // consumer pauses
      return false; // then exits early
    },
    { tracker, sinkCapacity: 1 },
  );

  await sleep(60);
  assert.equal(sinkCalls, 1, "sink is parked on its first element");
  // Upper bound on elements pulled from the source while the sink is paused:
  // 1 in the sink + 1 in sink.in + 1 per stage channel + 1 in flight per
  // stage + 1 in the producer.
  const bound = 1 + 1 + 3 + 3 + 1;
  assert.ok(pulled <= bound, `upstream pulled ${pulled}, bound is ${bound}`);
  assert.ok(pulled >= 3, `expected some progress before blocking, got ${pulled}`);

  openGate();
  const result = await runPromise;
  assert.equal(result.consumerStopped, true);
  expectCleanShutdown(tracker);
});

test("mid-stage failure releases every transferred element exactly once", async () => {
  const tracker = new ElementTracker();
  let pulled = 0;
  function* source(): Generator<number> {
    for (let i = 0; i < 30; i += 1) {
      pulled += 1;
      yield i;
    }
  }
  const flaky: StageDef = {
    name: "flaky",
    capacity: 2,
    fn: (el, ctx) => {
      if (el.value === 7) throw new Error("boom-7");
      return [ctx.create((el.value as number) * 10)];
    },
  };
  const pipeline = new Pipeline([
    mapStage("head", 1, (v) => v),
    flaky,
    mapStage("tail", 1, (v) => v),
  ]);
  const seen: number[] = [];
  await assert.rejects(
    pipeline.run(source(), (el) => {
      seen.push(el.value as number);
    }, { tracker }),
    /boom-7/,
  );
  assert.ok(pulled < 30, `failure must stop upstream pulls, pulled=${pulled}`);
  assert.ok(seen.length > 0, "elements before the failure were delivered");
  expectCleanShutdown(tracker);
});

test("early consumer exit unwinds the chain and releases everything", async () => {
  const tracker = new ElementTracker();
  let pulled = 0;
  function* source(): Generator<number> {
    for (let i = 0; i < 50; i += 1) {
      pulled += 1;
      yield i;
    }
  }
  const pipeline = new Pipeline([
    mapStage("a", 1, (v: number) => v + 1),
    mapStage("b", 2, (v: number) => v * 3),
    mapStage("c", 1, (v) => v),
  ]);
  const seen: number[] = [];
  const result = await pipeline.run(
    source(),
    (el) => {
      seen.push(el.value as number);
      return seen.length < 5 ? undefined : false;
    },
    { tracker, sinkCapacity: 1 },
  );
  assert.equal(result.consumerStopped, true);
  assert.equal(seen.length, 5);
  assert.ok(pulled < 50, `early exit must stop upstream pulls, pulled=${pulled}`);
  expectCleanShutdown(tracker);
});

test("sink throwing is treated as a failure and still cleans up", async () => {
  const tracker = new ElementTracker();
  const pipeline = new Pipeline([
    mapStage("x", 1, (v) => v),
    mapStage("y", 1, (v) => v),
    mapStage("z", 1, (v) => v),
  ]);
  await assert.rejects(
    pipeline.run(range(0, 10), () => {
      throw new Error("sink-broke");
    }, { tracker }),
    /sink-broke/,
  );
  expectCleanShutdown(tracker);
});
