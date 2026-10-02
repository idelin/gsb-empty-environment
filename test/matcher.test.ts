import { test } from "node:test";
import assert from "node:assert/strict";
import { Certificate } from "../src/certificate.ts";
import { Scheduler } from "../src/scheduler.ts";
import type { Shift } from "../src/scheduler.ts";

const cert = (over: Partial<ConstructorParameters<typeof Certificate>[0]> = {}) =>
  new Certificate({
    id: "cert-1",
    employeeId: "emp-1",
    skill: "welding",
    validFrom: 10,
    validTo: 100,
    ...over,
  });

const shift = (id: string, start: number, end: number, employeeId = "emp-1"): Shift => ({
  id,
  employeeId,
  requiredSkill: "welding",
  span: { start, end },
});

test("验收边界：持证 [10,100)，排班 [80,120) 被拒绝，缺口为 [100,120)", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  const result = s.schedule(shift("shift-1", 80, 120));
  assert.equal(result.ok, false);
  assert.deepEqual(result.ok === false && result.uncovered, [{ start: 100, end: 120 }]);
  assert.equal(s.scheduledShifts().length, 0);
});

test("边界：排班 [10,100) 与持证期完全重合（半开区间）被接受", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  const result = s.schedule(shift("shift-1", 10, 100));
  assert.equal(result.ok, true);
  assert.equal(s.scheduledShifts().length, 1);
});

test("排班完全落在持证期内被接受；早于生效期的部分被拒绝", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  assert.equal(s.schedule(shift("ok", 50, 80)).ok, true);
  const early = s.schedule(shift("bad", 0, 20));
  assert.equal(early.ok, false);
  assert.deepEqual(early.ok === false && early.uncovered, [{ start: 0, end: 10 }]);
});

test("双时态：排班时未知吊销则通过；知悉 60 追溯吊销后自动预警已排班次", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  s.advanceClock(10);
  // 排班时（事务时间 10）系统尚不知悉吊销，[50,80) 视为有效
  assert.equal(s.schedule(shift("shift-a", 50, 80)).ok, true);
  assert.equal(s.schedule(shift("shift-b", 20, 40)).ok, true);

  // 事务时间 30 收到通知：证书在有效时间 60 被吊销
  const warnings = s.revoke("cert-1", 60, 30);
  assert.equal(warnings.length, 1);
  assert.deepEqual(warnings[0], {
    shiftId: "shift-a",
    employeeId: "emp-1",
    requiredSkill: "welding",
    certificateId: "cert-1",
    revokedAt: 60,
    knownAt: 30,
    violated: [{ start: 60, end: 80 }],
  });
});

test("双时态快照：知悉时间之前查询仍显示证书有效", () => {
  const c = cert();
  c.revoke(60, 30); // 有效时间 60 吊销，事务时间 30 才知悉
  assert.deepEqual(c.effectiveInterval(29), { start: 10, end: 100 });
  assert.deepEqual(c.effectiveInterval(30), { start: 10, end: 60 });
});

test("复审期：reviewBy 提前截断有效截止", () => {
  const s = new Scheduler();
  s.addCertificate(cert({ reviewBy: 70 }));
  const result = s.schedule(shift("shift-1", 60, 90));
  assert.equal(result.ok, false);
  assert.deepEqual(result.ok === false && result.uncovered, [{ start: 70, end: 90 }]);
});

test("多证书并集覆盖：两段证书拼接覆盖整个班次", () => {
  const s = new Scheduler();
  s.addCertificate(cert({ id: "c1", validFrom: 10, validTo: 60 }));
  s.addCertificate(cert({ id: "c2", validFrom: 60, validTo: 120 }));
  assert.equal(s.schedule(shift("shift-1", 20, 110)).ok, true);
});

test("吊销只影响对应员工与技能，其他班次不受牵连", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  s.addCertificate(cert({ id: "cert-2", employeeId: "emp-2" }));
  s.schedule(shift("s1", 50, 80, "emp-1"));
  s.schedule(shift("s2", 50, 80, "emp-2"));
  const warnings = s.revoke("cert-1", 55, 30);
  assert.equal(warnings.length, 1);
  assert.equal(warnings[0].shiftId, "s1");
  assert.deepEqual(warnings[0].violated, [{ start: 55, end: 80 }]);
});

test("吊销点之后的班次整体违规，之前的班次不受影响", () => {
  const s = new Scheduler();
  s.addCertificate(cert());
  s.schedule(shift("before", 20, 50));
  s.schedule(shift("after", 70, 90));
  const warnings = s.revoke("cert-1", 60, 30);
  assert.equal(warnings.length, 1);
  assert.equal(warnings[0].shiftId, "after");
  assert.deepEqual(warnings[0].violated, [{ start: 70, end: 90 }]);
});

test("非法区间与未知证书报错", () => {
  const s = new Scheduler();
  assert.throws(() => s.schedule(shift("bad", 90, 90)), RangeError);
  assert.throws(() => s.revoke("nope", 1, 1), /未知证书/);
});
