import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CoroutinePool,
  type CoroutineBody,
  type SchedulerEvent,
} from "../src/scheduler.ts";

function makePool(
  options: { timeSlice?: number } = {},
): { pool: CoroutinePool; events: SchedulerEvent[] } {
  const events: SchedulerEvent[] = [];
  const pool = new CoroutinePool({
    timeSlice: options.timeSlice,
    onEvent: (event) => events.push(event),
  });
  return { pool, events };
}

function dispatches(events: SchedulerEvent[]): string[] {
  return events.filter((e) => e.type === "dispatch").map((e) => e.name);
}

test("验收边界：A(p1) 执行一次 Yield 后立即将 CPU 让给就绪的 B(p3)，B 完成后 A 继续", () => {
  const { pool, events } = makePool({ timeSlice: 8 });

  const bBody: CoroutineBody = function* () {
    yield;
    yield;
  };

  const aBody: CoroutineBody = function* (ctx) {
    // A 运行期间派生高优先级协程 B，B 进入就绪态。
    ctx.spawn({ name: "B", priority: 3 }, bBody);
    yield; // ← A 主动出让：CPU 必须立即交给 B
    yield;
    yield;
  };

  pool.spawn({ name: "A", priority: 1 }, aBody);
  pool.run();

  // 调度顺序：A 先跑一步并出让 → B 插队运行至完毕 → A 恢复运行。
  assert.deepEqual(dispatches(events), ["A", "B", "A"]);

  // A 出让后立刻发生针对 A 的高优先级抢占。
  const aYieldIndex = events.findIndex((e) => e.type === "yield" && e.name === "A");
  const preempt = events[aYieldIndex + 1];
  assert.deepEqual(preempt, {
    type: "preempt",
    name: "A",
    priority: 1,
    reason: "higher-priority-ready",
  });

  // 优先级反转严格控制：B 开始运行到 B 完成之间，A 不得执行任何一步。
  const bStart = events.findIndex((e) => e.type === "dispatch" && e.name === "B");
  const bDone = events.findIndex((e) => e.type === "complete" && e.name === "B");
  const aStepsWhileBActive = events
    .slice(bStart, bDone)
    .filter((e) => e.type === "step" && e.name === "A");
  assert.equal(aStepsWhileBActive.length, 0);

  // B 执行完毕后 A 继续运行直至完成，系统保持活性。
  const stats = pool.stats();
  assert.deepEqual(
    stats.map((s) => [s.name, s.totalSteps, s.done]),
    [
      ["A", 4, true],
      ["B", 3, true],
    ],
  );
});

test("严格优先级：高低优先级同时就绪时，高优先级整体先运行", () => {
  const { pool, events } = makePool();
  const low: CoroutineBody = function* () {
    yield;
    yield;
  };
  const high: CoroutineBody = function* () {
    yield;
  };
  pool.spawn({ name: "LOW", priority: 1 }, low);
  pool.spawn({ name: "HIGH", priority: 5 }, high);
  pool.run();

  assert.deepEqual(dispatches(events), ["HIGH", "LOW"]);
  const highDone = events.findIndex((e) => e.type === "complete" && e.name === "HIGH");
  const lowFirstStep = events.findIndex((e) => e.type === "step" && e.name === "LOW");
  assert.ok(highDone < lowFirstStep, "低优先级协程不得在高优先级完成前执行");
});

test("时间片轮转：同优先级协程按配额交替执行", () => {
  const { pool, events } = makePool({ timeSlice: 2 });
  const worker: CoroutineBody = function* () {
    yield;
    yield;
    yield;
    yield;
    yield;
  };
  pool.spawn({ name: "W1", priority: 2 }, worker);
  pool.spawn({ name: "W2", priority: 2 }, worker);
  pool.run();

  // 每个协程每次最多连续 2 步，然后轮转给另一个。
  assert.deepEqual(dispatches(events), ["W1", "W2", "W1", "W2", "W1", "W2"]);

  const sliceExhausted = events.filter(
    (e) => e.type === "preempt" && e.reason === "timeslice-exhausted",
  );
  assert.equal(sliceExhausted.length, 4);

  // 任意协程连续执行的步数不超过时间片配额。
  const stepNames = events.filter((e) => e.type === "step").map((e) => e.name);
  let runLength = 1;
  for (let i = 1; i <= stepNames.length; i += 1) {
    if (stepNames[i] === stepNames[i - 1]) {
      runLength += 1;
      assert.ok(runLength <= 2, "连续执行步数不得超过时间片配额");
    } else {
      runLength = 1;
    }
  }
});

test("配额监控：低优先级协程占用过多步数时被强制切换，保证活性", () => {
  const { pool, events } = makePool({ timeSlice: 3 });

  // 低优先级长任务：第 2 步派生高优先级协程。
  const hog: CoroutineBody = function* (ctx) {
    yield;
    ctx.spawn({ name: "URGENT", priority: 9 }, function* () {
      yield;
    });
    yield;
    yield;
    yield;
    yield;
    yield;
  };
  pool.spawn({ name: "HOG", priority: 1 }, hog);
  pool.run();

  // URGENT 一旦就绪，HOG 在下一步出让点立即被抢占。
  const urgentDispatch = events.findIndex((e) => e.type === "dispatch" && e.name === "URGENT");
  const hogStepsBeforeUrgent = events
    .slice(0, urgentDispatch)
    .filter((e) => e.type === "step" && e.name === "HOG").length;
  assert.equal(hogStepsBeforeUrgent, 2, "高优先级就绪后低优先级不得多占任何一步");

  // 所有协程最终完成：不会发生饥饿，系统活性得到保护。
  assert.ok(pool.stats().every((s) => s.done));
  assert.equal(pool.pending, 0);
});

test("非法时间片配额被拒绝", () => {
  assert.throws(() => new CoroutinePool({ timeSlice: 0 }), RangeError);
  assert.throws(() => new CoroutinePool({ timeSlice: 1.5 }), RangeError);
});
