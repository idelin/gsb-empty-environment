import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { WireReader } from "../src/reader.ts";
import { WireWriter } from "../src/writer.ts";
import { Simulation } from "../src/sim/simulation.ts";
import { recordTrace } from "../src/sim/replay.ts";
import { buildQueueSimulation } from "../src/sim/workload.ts";

describe("strict no-leak assertions", () => {
  it("simulation retains nothing after dispose()", () => {
    const sim = buildQueueSimulation(21, true);
    sim.run(10_000);
    assert.ok(sim.pendingEvents > 0, "workload keeps generating events");
    sim.dispose();
    assert.equal(sim.pendingEvents, 0);
    assert.equal(sim.handlerCount, 0);
    assert.deepEqual(sim.getTrace(), []);
  });

  it("run() resets the re-entrancy guard even when a handler throws", () => {
    const sim = new Simulation({ seed: 1 });
    sim.on("boom", () => {
      throw new Error("handler failure");
    });
    sim.schedule(0, "boom");
    assert.throws(() => sim.run(1), /handler failure/);
    // Guard must be released: a subsequent run works normally.
    sim.schedule(0, "ok");
    const stats = sim.run(10);
    assert.equal(stats.steps, 1);
  });

  it("reader release() drops the underlying buffer reference", () => {
    const w = new WireWriter();
    w.varintField(1, 1);
    const r = new WireReader(w.finish());
    r.release();
    assert.throws(() => r.readVarint(), /released/);
    assert.throws(() => r.readTag(), /released/);
  });

  it("sub-reader views cannot outlive their declared bound", () => {
    const w = new WireWriter();
    w.messageField(1, (inner) => inner.varintField(1, 5).varintField(2, 6));
    const r = new WireReader(w.finish());
    r.readTag();
    const sub = r.subReader();
    sub.readTag();
    sub.readVarint();
    sub.readTag();
    sub.readVarint();
    assert.ok(sub.eof);
    assert.equal(sub.readTag(), null, "no phantom fields past the nested bound");
  });

  it("writer reset() reuses capacity without retaining old bytes", () => {
    const w = new WireWriter();
    w.varintField(1, 999);
    const first = w.finish();
    w.reset();
    w.varintField(1, 1);
    const second = w.finish();
    assert.notDeepEqual([...second], [...first]);
    assert.equal(second.length < first.length || first.length === second.length, true);
  });

  it("heap memory stays bounded across repeated million-event runs", () => {
    assert.equal(typeof global.gc, "function", "run tests with NODE_OPTIONS=--expose-gc");
    global.gc();
    const before = process.memoryUsage().heapUsed;
    for (let i = 0; i < 3; i++) {
      const sim = buildQueueSimulation(100 + i, true);
      recordTrace(sim, 200_000);
      sim.dispose();
    }
    global.gc();
    const after = process.memoryUsage().heapUsed;
    // Trace arrays are the dominant allocation; after disposal + GC the heap
    // must not grow by more than a small constant slack.
    assert.ok(
      after - before < 32 * 1024 * 1024,
      `heap grew by ${(after - before) / 1e6}MB across runs`,
    );
  });

  it("no dangling listeners: handler map empties after unsubscribe", () => {
    const sim = new Simulation({ seed: 1 });
    const offs = [sim.on("a", () => {}), sim.on("a", () => {}), sim.on("b", () => {})];
    assert.equal(sim.handlerCount, 3);
    for (const off of offs) off();
    assert.equal(sim.handlerCount, 0);
  });
});
