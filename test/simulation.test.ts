import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Scheduler } from "../src/sim/scheduler.ts";
import { Simulation } from "../src/sim/simulation.ts";
import { Prng } from "../src/sim/prng.ts";
import {
  deserializeTrace,
  recordTrace,
  serializeTrace,
  verifyReplay,
} from "../src/sim/replay.ts";
import { buildQueueSimulation } from "../src/sim/workload.ts";

// Golden fingerprints captured from the reference implementation. Any change
// in event ordering, PRNG stream, or hashing breaks these constants loudly.
const GOLDEN_1M_SEED42 = 1112399500;
const GOLDEN_1M_SEED43 = 3762455364;

describe("scheduler ordering", () => {
  it("fires same-time events in strict FIFO schedule order", () => {
    const s = new Scheduler();
    s.schedule(0, "c", null, 0);
    s.schedule(0, "a", null, 0);
    s.schedule(0, "b", null, 0);
    assert.deepEqual([s.pop()?.type, s.pop()?.type, s.pop()?.type], ["c", "a", "b"]);
  });

  it("orders by (time, seq) across a randomized workload", () => {
    const prng = new Prng(7);
    const s = new Scheduler();
    const n = 5000;
    for (let i = 0; i < n; i++) s.schedule(prng.nextInt(50), "e", i, 0);
    let lastTime = -1;
    let lastSeq = -1;
    let count = 0;
    for (let ev = s.pop(); ev; ev = s.pop()) {
      assert.ok(ev.time > lastTime || (ev.time === lastTime && ev.seq > lastSeq));
      lastTime = ev.time;
      lastSeq = ev.seq;
      count++;
    }
    assert.equal(count, n);
    assert.equal(s.size, 0);
  });

  it("rejects negative or non-integer delays", () => {
    const s = new Scheduler();
    assert.throws(() => s.schedule(-1, "x", null, 0), RangeError);
    assert.throws(() => s.schedule(0.5, "x", null, 0), RangeError);
  });
});

describe("deterministic regression", () => {
  it("runs one million steps with a stable golden hash (seed 42)", () => {
    const stats = buildQueueSimulation(42).run(1_000_000);
    assert.equal(stats.steps, 1_000_000);
    assert.equal(stats.finalHash, GOLDEN_1M_SEED42);
  });

  it("produces bit-identical results across repeated runs", () => {
    const a = buildQueueSimulation(42).run(1_000_000);
    const b = buildQueueSimulation(42).run(1_000_000);
    assert.deepEqual(a, b);
  });

  it("diverges for a different seed (seed 43 golden hash)", () => {
    const stats = buildQueueSimulation(43).run(1_000_000);
    assert.equal(stats.finalHash, GOLDEN_1M_SEED43);
    assert.notEqual(stats.finalHash, GOLDEN_1M_SEED42);
  });

  it("events scheduled from inside handlers land in deterministic order", () => {
    const order: string[] = [];
    const sim = new Simulation({ seed: 1 });
    sim.on("kick", (s) => {
      order.push("kick");
      s.schedule(0, "same-time");
      s.schedule(1, "later");
    });
    sim.on("same-time", () => order.push("same-time"));
    sim.on("later", () => order.push("later"));
    sim.schedule(0, "kick");
    sim.run(10);
    assert.deepEqual(order, ["kick", "same-time", "later"]);
    assert.equal(sim.time, 1);
  });
});

describe("bug reproduction via record & replay", () => {
  it("replays a recorded trace and verifies every step", () => {
    const record = recordTrace(buildQueueSimulation(99, true), 20_000);
    const result = verifyReplay(() => buildQueueSimulation(99, true), record);
    assert.ok(result.ok);
    assert.equal(result.stepsCompared, 20_000);
    assert.equal(result.divergence, null);
  });

  it("survives protobuf serialization round-trip (nested + packed fields)", () => {
    const record = recordTrace(buildQueueSimulation(7, true), 5_000, "rt");
    const restored = deserializeTrace(serializeTrace(record));
    assert.deepEqual(restored, record);
    const result = verifyReplay(() => buildQueueSimulation(7, true), restored);
    assert.ok(result.ok);
  });

  it("pinpoints the exact step of an injected fault", () => {
    const record = recordTrace(buildQueueSimulation(11, true), 10_000);
    const target = 4321;
    record.entries[target] = { ...record.entries[target], hash: record.entries[target].hash ^ 0x1 };
    const result = verifyReplay(() => buildQueueSimulation(11, true), record);
    assert.equal(result.ok, false);
    assert.equal(result.divergence?.step, target + 1);
  });

  it("detects a mutated simulation (bug) during replay", () => {
    const record = recordTrace(buildQueueSimulation(5, true), 5_000);
    // "Buggy" build: service time distribution altered.
    const buggy = () => {
      const sim = buildQueueSimulation(5, true);
      sim.on("service", (s) => s.schedule(0, "phantom"));
      return sim;
    };
    const result = verifyReplay(buggy, record);
    assert.equal(result.ok, false);
    assert.ok(result.divergence !== null);
  });

  it("detects a trace longer than the simulation can reproduce", () => {
    const record = recordTrace(buildQueueSimulation(3, true), 100);
    // Factory whose queue drains immediately: no handlers at all.
    const result = verifyReplay(() => new Simulation({ seed: 3, recordTrace: true }), record);
    assert.equal(result.ok, false);
    assert.equal(result.divergence?.actualType, null);
  });
});

describe("concurrency safety", () => {
  it("interleaved async runs stay bit-for-bit deterministic", async () => {
    const seeds = [1, 2, 3, 4, 5, 6, 7, 8];
    const sequential = seeds.map((s) => buildQueueSimulation(s).run(50_000).finalHash);
    const interleaved = await Promise.all(
      seeds.map((s) => buildQueueSimulation(s).runAsync(50_000, 64).then((r) => r.finalHash)),
    );
    assert.deepEqual(interleaved, sequential);
  });

  it("rejects re-entrant run() while running", () => {
    const sim = new Simulation({ seed: 1 });
    let reentered: Error | null = null;
    sim.on("e", (s) => {
      try {
        s.run(1);
      } catch (err) {
        reentered = err as Error;
      }
    });
    sim.schedule(0, "e");
    sim.run(10);
    assert.match(String(reentered), /already running/);
  });

  it("rejects re-entrant runAsync() while running", async () => {
    const sim = new Simulation({ seed: 1 });
    let observed: Error | null = null;
    sim.on("e", (s) => {
      s.runAsync(1).catch((err) => {
        observed = err as Error;
      });
    });
    sim.schedule(0, "e");
    await sim.runAsync(10);
    await new Promise((r) => setImmediate(r));
    assert.match(String(observed), /already running/);
  });

  it("unsubscribed handlers stop receiving events immediately", () => {
    const sim = new Simulation({ seed: 1 });
    let hits = 0;
    const off = sim.on("e", () => hits++);
    sim.on("e", () => off()); // removes the first handler mid-dispatch
    sim.schedule(0, "e");
    sim.schedule(1, "e");
    sim.run(10);
    assert.equal(hits, 1);
  });
});
