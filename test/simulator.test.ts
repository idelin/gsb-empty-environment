import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { simulate } from "../src/simulator.ts";
import { validateScenario } from "../src/validate.ts";
import type { Scenario } from "../src/types.ts";

const here = dirname(fileURLToPath(import.meta.url));

function loadExample(name: string): Scenario {
  const raw = JSON.parse(readFileSync(join(here, "..", "examples", name), "utf8"));
  return validateScenario(raw);
}

let passed = 0;
function test(name: string, fn: () => void): void {
  fn();
  passed += 1;
  console.log(`ok - ${name}`);
}

test("basic preemption and idle fast-forward", () => {
  const res = simulate(loadExample("basic-preemption.json"));
  assert.equal(res.status, "completed");
  const byId = new Map(res.tasks.map((t) => [t.id, t]));
  assert.equal(byId.get("T2")!.completedAt, 2, "T2 preempts T1 at t=2 and finishes");
  assert.equal(byId.get("T1")!.completedAt, 3);
  assert.equal(byId.get("T3")!.completedAt, 8);
  const idle = res.ticks.find((r) => r.idle);
  assert.ok(idle, "an idle record exists while waiting for T3");
  assert.equal(idle.time, 4);
  assert.equal(idle.duration, 4, "idle fast-forwards t=4..7");
  assert.equal(idle.deadlockCycles.length, 0, "plain idle has no deadlock cycle");
});

test("transitive priority inheritance along a lock chain", () => {
  const res = simulate(loadExample("priority-inheritance.json"));
  assert.equal(res.status, "completed");
  // TH(3) blocks on A@TL; TL blocks on B@TM  =>  TM must transitively inherit 3
  const tmBoosted = res.ticks.find(
    (r) => r.running?.id === "TM" && r.inherited.some((i) => i.id === "TM" && i.eff === 3),
  );
  assert.ok(tmBoosted, "TM runs with transitively inherited priority 3");
  const donor = tmBoosted.inherited.find((i) => i.id === "TM")!;
  assert.equal(donor.from, "TL", "TM inherits directly from TL (which inherited from TH)");
  const tlBoosted = res.ticks.find((r) => r.inherited.some((i) => i.id === "TL" && i.eff === 3));
  assert.ok(tlBoosted, "TL inherits priority 3 from TH");
  // 锁全部释放后，有效优先级回落到基础优先级
  const last = res.ticks[res.ticks.length - 1]!;
  assert.equal(last.inherited.length, 0, "after locks are released, inheritance is gone");
  assert.ok(res.tasks.every((t) => t.state === "done" && !t.aborted));
});

test("lock wait timeout: abort exits the waiter, continue skips the acquire", () => {
  const res = simulate(loadExample("timeout.json"));
  assert.equal(res.status, "completed");
  const byId = new Map(res.tasks.map((t) => [t.id, t]));
  assert.equal(byId.get("T2")!.aborted, true, "T2 times out and aborts");
  assert.equal(byId.get("T2")!.completedAt, 4, "T2 aborts after 3 ticks of waiting");
  assert.equal(byId.get("T1")!.aborted, false);
  assert.equal(byId.get("T3")!.aborted, false);
  assert.equal(byId.get("T3")!.state, "done", "T3 eventually acquires A and completes");
  const timeoutTick = res.ticks.find((r) =>
    r.events.some((e) => e.includes("T2 timed out waiting for lock A")),
  );
  assert.ok(timeoutTick, "timeout event is reported on its tick");
});

test("deadlock is detected as a lock-wait cycle, not as idle", () => {
  const res = simulate(loadExample("deadlock.json"));
  assert.equal(res.status, "deadlock");
  assert.equal(res.deadlocks.length, 1);
  assert.deepEqual(new Set(res.deadlocks[0]!.slice(0, -1)), new Set(["T1", "T2"]));
  const last = res.ticks[res.ticks.length - 1]!;
  assert.equal(last.idle, false, "final stalled tick must not be reported as idle");
  assert.ok(last.deadlockCycles.length > 0, "final tick carries the wait cycle");
  assert.ok(
    last.events.some((e) => e.includes("permanent deadlock")),
    "permanent deadlock event is emitted",
  );
  assert.ok(res.tasks.every((t) => t.state === "blocked"), "both tasks remain blocked");
});

test("three-task wait cycle is detected", () => {
  const scenario: Scenario = {
    name: "3-cycle",
    tasks: [
      {
        id: "T1",
        priority: 1,
        segments: [
          { type: "acquire", lock: "A" },
          { type: "compute", duration: 5 },
          { type: "acquire", lock: "B" },
        ],
      },
      {
        id: "T2",
        priority: 2,
        releaseTime: 1,
        segments: [
          { type: "acquire", lock: "B" },
          { type: "compute", duration: 5 },
          { type: "acquire", lock: "C" },
        ],
      },
      {
        id: "T3",
        priority: 3,
        releaseTime: 2,
        segments: [
          { type: "acquire", lock: "C" },
          { type: "compute", duration: 1 },
          { type: "acquire", lock: "A" },
        ],
      },
    ],
  };
  const res = simulate(scenario);
  assert.equal(res.status, "deadlock");
  assert.equal(res.deadlocks.length, 1);
  assert.deepEqual(new Set(res.deadlocks[0]!.slice(0, -1)), new Set(["T1", "T2", "T3"]));
  // 传递继承也出现在环形成之前：T2 曾通过 T1 传递继承 T3 的优先级 3
  const boosted = res.ticks.find((r) =>
    r.inherited.some((i) => i.id === "T2" && i.eff === 3 && i.from === "T1"),
  );
  assert.ok(boosted, "T2 transitively inherits 3 from T3 via T1");
});

test("timeout can break a deadlock cycle", () => {
  const scenario: Scenario = {
    name: "deadlock broken by timeout",
    tasks: [
      {
        id: "T1",
        priority: 2,
        segments: [
          { type: "acquire", lock: "A" },
          { type: "compute", duration: 2 },
          { type: "acquire", lock: "B", timeout: 3, onTimeout: "abort" },
        ],
      },
      {
        id: "T2",
        priority: 3,
        releaseTime: 1,
        segments: [
          { type: "acquire", lock: "B" },
          { type: "compute", duration: 2 },
          { type: "acquire", lock: "A" },
          { type: "compute", duration: 1 },
          { type: "release", "lock": "A" },
          { type: "release", "lock": "B" },
        ],
      },
    ],
  };
  const res = simulate(scenario);
  assert.equal(res.status, "completed", "timeout abort releases lock A and unblocks T2");
  assert.equal(res.deadlocks.length, 1, "the cycle was still detected and reported");
  const byId = new Map(res.tasks.map((t) => [t.id, t]));
  assert.equal(byId.get("T1")!.aborted, true);
  assert.equal(byId.get("T2")!.state, "done");
});

test("scenario validation rejects bad input", () => {
  assert.throws(
    () =>
      validateScenario({
        tasks: [
          { id: "X", priority: 1, segments: [{ type: "compute", duration: 1 }] },
          { id: "X", priority: 2, segments: [{ type: "compute", duration: 1 }] },
        ],
      }),
    /duplicate task id/,
  );
  assert.throws(
    () =>
      validateScenario({
        tasks: [{ id: "X", priority: 1, segments: [{ type: "acquire" }] }],
      }),
    /acquire\.lock/,
  );
});

console.log(`\n${passed} tests passed`);
