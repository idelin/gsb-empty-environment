import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";

function makeMaster(totalQps = 3000) {
  const clock = new FakeClock();
  const master = new MasterCoordinator({ totalQps, windowMs: 1000, clock });
  return { clock, master };
}

test("Master：池子耗尽后，新请求从持有空闲配额的 worker 回收调拨", () => {
  const { master } = makeMaster();
  master.registerWorker("W1");
  master.registerWorker("W2");
  const r1 = master.heartbeat({ workerId: "W1", used: 0, requested: 3000 });
  assert.equal(r1.granted, 3000);
  const r2 = master.heartbeat({ workerId: "W2", used: 0, requested: 1000 });
  assert.equal(r2.granted, 1000); // W1 完全空闲，其 1000 空余额度被调拨给 W2
  assert.deepEqual(master.reallocations, [
    { from: "W1", to: "W2", amount: 1000, at: 0 },
  ]);
  assert.equal(master.available(), 0);
});

test("Master：活跃 worker 的配额受保护，不可被回收", () => {
  const { master } = makeMaster();
  master.registerWorker("W1");
  master.registerWorker("W2");
  master.heartbeat({ workerId: "W1", used: 0, requested: 3000 });
  master.heartbeat({ workerId: "W1", used: 3000, requested: 0 }); // W1 满负荷
  const r = master.heartbeat({ workerId: "W2", used: 0, requested: 1000 });
  assert.equal(r.granted, 0);
  assert.equal(master.reallocations.length, 0);
});

test("Master：重复注册与未知 worker 报错", () => {
  const { master } = makeMaster();
  master.registerWorker("W1");
  assert.throws(() => master.registerWorker("W1"), /already registered/);
  assert.throws(
    () => master.heartbeat({ workerId: "ghost", used: 0, requested: 1 }),
    /unknown worker/,
  );
});

test("Master：租约过期后未消耗额度自动归还池中", () => {
  const { clock, master } = makeMaster();
  master.registerWorker("W1");
  master.registerWorker("W2");
  master.heartbeat({ workerId: "W1", used: 0, requested: 3000 });
  assert.equal(master.available(), 0);

  clock.advance(1001); // W1 租约（TTL=1000ms）过期且未续订
  const r = master.heartbeat({ workerId: "W2", used: 0, requested: 1000 });
  assert.equal(r.granted, 1000);
  assert.equal(master.snapshot().workers.find((w) => w.id === "W1")!.allocation, 0);
});

test("Master：窗口滑过后已消耗额度回收，可重新分配", () => {
  const { clock, master } = makeMaster();
  master.registerWorker("W1");
  master.heartbeat({ workerId: "W1", used: 0, requested: 3000 });
  master.heartbeat({ workerId: "W1", used: 3000, requested: 0 });
  assert.equal(master.available(), 0); // 消耗仍计入当前窗口

  clock.advance(1100); // 窗口滑过，消耗记录衰减
  assert.equal(master.available(), 3000);
});

test("Master：优先从空闲量最大的 worker 回收调拨", () => {
  const { master } = makeMaster();
  for (const id of ["A", "B", "C"]) master.registerWorker(id);
  for (const id of ["A", "B", "C"]) {
    master.heartbeat({ workerId: id, used: 0, requested: 1000 });
  }
  // B 上报消耗 400（空闲 600），C 完全空闲（空闲 1000）
  master.heartbeat({ workerId: "B", used: 400, requested: 0 });

  const r = master.heartbeat({ workerId: "A", used: 1000, requested: 1000 });
  assert.equal(r.granted, 1000);
  assert.deepEqual(master.reallocations, [
    { from: "C", to: "A", amount: 1000, at: 0 },
  ]);
  const snap = master.snapshot();
  assert.equal(snap.workers.find((w) => w.id === "C")!.allocation, 0);
  assert.equal(snap.workers.find((w) => w.id === "B")!.allocation, 600);
});
