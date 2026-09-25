import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ControllableScheduler } from "../src/scheduler.ts";

describe("ControllableScheduler", () => {
  it("executes jobs only when pumped, in FIFO order", async () => {
    const scheduler = new ControllableScheduler();
    const a = scheduler.enqueue("a", () => 1);
    const b = scheduler.enqueue("b", () => 2);
    assert.equal(scheduler.pendingCount, 2);
    assert.equal(a.started, false);
    await scheduler.pump(1);
    assert.equal(a.started, true);
    assert.equal(b.started, false);
    assert.deepEqual(scheduler.executionLog, ["a"]);
    await scheduler.pumpAll();
    assert.deepEqual(scheduler.executionLog, ["a", "b"]);
    assert.equal(await a.promise, 1);
    assert.equal(await b.promise, 2);
  });

  it("runs jobs enqueued during a pump when draining", async () => {
    const scheduler = new ControllableScheduler();
    const outer = scheduler.enqueue("outer", async () => {
      scheduler.enqueue("inner", () => "done");
    });
    await scheduler.pumpAll();
    assert.deepEqual(scheduler.executionLog, ["outer", "inner"]);
    await outer.promise;
  });

  it("reproduces a stale reply: old response delivered after state moved on", async () => {
    const scheduler = new ControllableScheduler();
    let currentVersion = 1;
    const accepted: number[] = [];
    // A slow backend reply captured at request time, delivered later.
    const reply = scheduler.enqueue("reply:v1", () => ({ version: 1 }));
    currentVersion = 2; // hot update lands before the reply is delivered
    await scheduler.pumpAll();
    const stale = await reply.promise;
    if (stale.version === currentVersion) accepted.push(stale.version);
    assert.deepEqual(accepted, [], "stale reply is rejected by the version check");
  });

  it("propagates job failures to the task promise", async () => {
    const scheduler = new ControllableScheduler();
    const failing = scheduler.enqueue("boom", () => {
      throw new Error("shaping failed");
    });
    const assertion = assert.rejects(failing.promise, /shaping failed/);
    await scheduler.pumpAll();
    await assertion;
  });
});
