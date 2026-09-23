import { test } from "node:test";
import assert from "node:assert/strict";
import { Scheduler } from "../src/index.ts";

test("tasks run FIFO only when pumped", async () => {
  const scheduler = new Scheduler();
  const ran: string[] = [];
  const a = scheduler.task(() => {
    ran.push("a");
    return 1;
  });
  const b = scheduler.task(() => {
    ran.push("b");
    return 2;
  });
  assert.equal(scheduler.pending, 2);
  assert.deepEqual(ran, []);
  assert.equal(await scheduler.pump(1), 1);
  assert.deepEqual(ran, ["a"]);
  await scheduler.runUntilIdle();
  assert.deepEqual(ran, ["a", "b"]);
  assert.equal(await a, 1);
  assert.equal(await b, 2);
  assert.equal(scheduler.completed, 2);
});

test("runUntilIdle drains tasks enqueued while draining", async () => {
  const scheduler = new Scheduler();
  const ran: string[] = [];
  void scheduler.task(() => {
    ran.push("outer");
    void scheduler.task(() => {
      ran.push("inner");
    });
  });
  await scheduler.runUntilIdle();
  assert.deepEqual(ran, ["outer", "inner"]);
});

test("task rejection propagates to the returned promise", async () => {
  const scheduler = new Scheduler();
  const failing = scheduler.task(() => {
    throw new Error("boom");
  });
  const observed = failing.then(
    () => "ok",
    (err: Error) => err.message,
  );
  await scheduler.runUntilIdle();
  assert.equal(await observed, "boom");
});

test("auto scheduler drains without manual pumping", async () => {
  const scheduler = new Scheduler({ auto: true });
  const value = await scheduler.task(() => 42);
  assert.equal(value, 42);
});
