import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { MasterCoordinator } from "../src/master.ts";

test("Master 将配额池平分给 3 个 Worker", () => {
  const clock = new ManualClock();
  const master = new MasterCoordinator({ pool: 3000, windowMs: 1000, clock });

  for (const id of ["A", "B", "C"]) {
    const res = master.handleHeartbeat({ workerId: id, usedTotal: 0, requested: 1000 });
    assert.equal(res.allocated, 1000);
  }
  assert.equal(master.poolFree, 0);
  assert.equal(master.totalAllocated, 3000);
});

test("配额池不足时授予部分额度", () => {
  const clock = new ManualClock();
  const master = new MasterCoordinator({ pool: 3000, windowMs: 1000, clock });

  // A 已全额用完 2000（无空闲余额可调拨），池内仅剩 1000
  master.handleHeartbeat({ workerId: "A", usedTotal: 2000, requested: 2000 });
  const res = master.handleHeartbeat({ workerId: "B", usedTotal: 0, requested: 1500 });
  assert.equal(res.allocated, 1000);
  assert.equal(master.poolFree, 0);
});

test("空闲池不足时优先从用量最少的空闲 Worker 调拨", () => {
  const clock = new ManualClock();
  const master = new MasterCoordinator({ pool: 3000, windowMs: 1000, clock });

  for (const id of ["A", "B", "C"]) {
    master.handleHeartbeat({ workerId: id, usedTotal: 0, requested: 1000 });
  }
  // B 半忙：上报已用 500；C 完全空闲
  master.handleHeartbeat({ workerId: "B", usedTotal: 500, requested: 0 });

  // A 耗尽自身配额，批量续订 1000 -> 应从 C 调拨而非 B
  const res = master.handleHeartbeat({ workerId: "A", usedTotal: 1000, requested: 1000 });
  assert.equal(res.allocated, 2000);
  assert.equal(master.allocatedOf("C"), 0);
  assert.equal(master.allocatedOf("B"), 1000);
  assert.deepEqual(master.reclaims, [{ from: "C", to: "A", tokens: 1000, at: 0 }]);

  // A 再次续订：C 已无余额，改从 B 的未使用部分调拨 500
  const res2 = master.handleHeartbeat({ workerId: "A", usedTotal: 2000, requested: 1000 });
  assert.equal(res2.allocated, 2500);
  assert.equal(master.allocatedOf("B"), 500);
  assert.deepEqual(master.reclaims[1], { from: "B", to: "A", tokens: 500, at: 0 });

  // 全池耗尽：再申请只能拿到 0
  const res3 = master.handleHeartbeat({ workerId: "A", usedTotal: 2500, requested: 1000 });
  assert.equal(res3.allocated, 2500);
  assert.equal(master.totalAllocated, 3000);
});

test("滑动窗口推进后租约失效、配额池重置", () => {
  const clock = new ManualClock();
  const master = new MasterCoordinator({ pool: 3000, windowMs: 1000, clock });

  for (const id of ["A", "B", "C"]) {
    master.handleHeartbeat({ workerId: id, usedTotal: 0, requested: 1000 });
  }
  assert.equal(master.poolFree, 0);

  clock.advance(1000);
  const res = master.handleHeartbeat({ workerId: "A", usedTotal: 0, requested: 1000 });
  assert.equal(res.allocated, 1000);
  assert.equal(res.windowStart, 1000);
  assert.equal(res.expiresAt, 2000);
  assert.equal(master.poolFree, 2000);
  assert.equal(master.allocatedOf("B"), 0);
});
