import { test } from "node:test";
import assert from "node:assert/strict";
import { CoroutinePool } from "./scheduler.ts";

test("验收边界：A(prio=1) 一次 Yield 立即让 CPU 给就绪的 B(prio=3)，B 完成后 A 继续", () => {
  const pool = new CoroutinePool({ timeSlice: 2 });

  function* a(): Generator<string, void, void> {
    yield "A:step-1";
    pool.spawn("B", 3, b); // B 进入就绪态
    yield "A:yield"; // 主动出让
    yield "A:step-2";
  }
  function* b(): Generator<string, void, void> {
    yield "B:step-1";
    yield "B:step-2";
  }

  pool.spawn("A", 1, a);
  const trace = pool.run();

  assert.deepEqual(trace, [
    "A:step-1",
    "A:yield",
    "B:step-1",
    "B:step-2",
    "A:step-2",
  ]);
  assert.deepEqual(pool.events, ["preempt:A", "done:B", "done:A"]);

  const statA = pool.stats().find((s) => s.id === "A")!;
  assert.equal(statA.preemptions, 1);
  assert.equal(statA.done, true);
});

test("优先级反转控制：高优先级协程超出时间片仍连续运行至完成，不被低优先级打断", () => {
  const pool = new CoroutinePool({ timeSlice: 2 });

  function* low(): Generator<string, void, void> {
    yield "L:1";
    yield "L:2";
  }
  function* high(): Generator<string, void, void> {
    for (let i = 1; i <= 5; i++) yield `H:${i}`; // 远超时间片配额
  }

  pool.spawn("L", 1, low);
  pool.spawn("H", 9, high);
  const trace = pool.run();

  assert.deepEqual(trace, ["H:1", "H:2", "H:3", "H:4", "H:5", "L:1", "L:2"]);
  assert.equal(pool.events.filter((e) => e.startsWith("rotate:H")).length, 0);
});

test("时间片轮转：同优先级协程按配额交替执行，强制协同切换保护活性", () => {
  const pool = new CoroutinePool({ timeSlice: 2 });

  function* c(): Generator<string, void, void> {
    for (let i = 1; i <= 5; i++) yield `C:${i}`;
  }
  function* d(): Generator<string, void, void> {
    for (let i = 1; i <= 5; i++) yield `D:${i}`;
  }

  pool.spawn("C", 1, c);
  pool.spawn("D", 1, d);
  const trace = pool.run();

  assert.deepEqual(trace, [
    "C:1", "C:2",
    "D:1", "D:2",
    "C:3", "C:4",
    "D:3", "D:4",
    "C:5",
    "D:5",
  ]);
  assert.deepEqual(
    pool.events.filter((e) => e.startsWith("rotate:")),
    ["rotate:C", "rotate:D", "rotate:C", "rotate:D"],
  );
});

test("配额监控：低优先级长协程独占时持续运行，同优先级伙伴出现后按配额让位", () => {
  const pool = new CoroutinePool({ timeSlice: 3 });

  function* hog(): Generator<string, void, void> {
    yield "hog:1";
    yield "hog:2";
    pool.spawn("peer", 1, peer); // 运行中新增同优先级协程
    yield "hog:3"; // 配额(3)耗尽 -> 强制切换给 peer
    yield "hog:4";
  }
  function* peer(): Generator<string, void, void> {
    yield "peer:1";
    yield "peer:2";
  }

  pool.spawn("hog", 1, hog);
  const trace = pool.run();

  assert.deepEqual(trace, ["hog:1", "hog:2", "hog:3", "peer:1", "peer:2", "hog:4"]);
  const hogStat = pool.stats().find((s) => s.id === "hog")!;
  assert.equal(hogStat.rotations, 1);
  assert.equal(hogStat.steps, 4);
});

test("调度顺序：就绪队列按优先级降序、同优先级先进先出", () => {
  const pool = new CoroutinePool();

  function* x(): Generator<string, void, void> { yield "X"; }
  function* y(): Generator<string, void, void> { yield "Y"; }
  function* z(): Generator<string, void, void> { yield "Z"; }

  pool.spawn("X", 1, x);
  pool.spawn("Y", 5, y);
  pool.spawn("Z", 5, z);

  assert.deepEqual(pool.run(), ["Y", "Z", "X"]);
});

test("参数校验：非法时间片与重复 id 抛错", () => {
  assert.throws(() => new CoroutinePool({ timeSlice: 0 }), RangeError);
  const pool = new CoroutinePool();
  function* noop(): Generator<string, void, void> { yield "n"; }
  pool.spawn("dup", 1, noop);
  assert.throws(() => pool.spawn("dup", 1, noop), /duplicate coroutine id/);
});
