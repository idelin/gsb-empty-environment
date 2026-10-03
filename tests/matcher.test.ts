import test from "node:test";
import assert from "node:assert/strict";
import { QualificationMatcher } from "../src/matcher.ts";
import type { Shift } from "../src/matcher.ts";

const CERT = {
  id: "cert-1",
  employeeId: "emp-1",
  skill: "forklift",
  validFrom: 10,
  validTo: 100,
};

function makeMatcher(): QualificationMatcher {
  const matcher = new QualificationMatcher();
  matcher.issueCertificate({ ...CERT });
  return matcher;
}

function shift(id: string, start: number, end: number): Shift {
  return { id, employeeId: "emp-1", skill: "forklift", start, end };
}

test("验收边界：持证 [10,100)，排班 [80,120) 因 [100,120) 缺资质被拒绝", () => {
  const matcher = makeMatcher();
  const result = matcher.scheduleShift(shift("S1", 80, 120), 0);
  assert.equal(result.ok, false);
  assert.deepEqual(result.missing, [{ start: 100, end: 120 }]);
  assert.deepEqual(matcher.listScheduled(), []);
});

test("验收边界：排班 [80,100) 恰好落在有效期内，允许排班", () => {
  const matcher = makeMatcher();
  const result = matcher.scheduleShift(shift("S1", 80, 100), 0);
  assert.equal(result.ok, true);
  assert.deepEqual(result.missing, []);
  assert.equal(matcher.listScheduled().length, 1);
});

test("边界：排班端点与证书端点重合（半开区间）", () => {
  const matcher = makeMatcher();
  assert.equal(matcher.validateShift(shift("S1", 10, 100), 0).ok, true);
  assert.equal(matcher.validateShift(shift("S2", 0, 10), 0).ok, false);
  assert.deepEqual(matcher.validateShift(shift("S3", 100, 110), 0).missing, [
    { start: 100, end: 110 },
  ]);
});

test("缺口计算：班次跨越证书生效起点，输出前段缺口", () => {
  const matcher = makeMatcher();
  const result = matcher.validateShift(shift("S1", 0, 50), 0);
  assert.deepEqual(result.missing, [{ start: 0, end: 10 }]);
});

test("缺口计算：班次完全不在有效期内，整段缺失", () => {
  const matcher = makeMatcher();
  const result = matcher.validateShift(shift("S1", 200, 300), 0);
  assert.deepEqual(result.missing, [{ start: 200, end: 300 }]);
});

test("多证书拼接覆盖整个班次", () => {
  const matcher = makeMatcher();
  matcher.issueCertificate({
    id: "cert-2",
    employeeId: "emp-1",
    skill: "forklift",
    validFrom: 100,
    validTo: 200,
  });
  assert.equal(matcher.validateShift(shift("S1", 80, 120), 0).ok, true);
});

test("复审延期：复审后排班 [80,120) 可通过", () => {
  const matcher = makeMatcher();
  matcher.recordReview("cert-1", 150, 50);
  const result = matcher.scheduleShift(shift("S1", 80, 120), 60);
  assert.equal(result.ok, true);
});

test("双时态：复审知悉时间之前的查询不受复审影响", () => {
  const matcher = makeMatcher();
  matcher.recordReview("cert-1", 150, 50);
  assert.deepEqual(matcher.effectiveValidity("cert-1", 49), { start: 10, end: 100 });
  assert.deepEqual(matcher.effectiveValidity("cert-1", 50), { start: 10, end: 150 });
  assert.equal(matcher.validateShift(shift("S1", 80, 120), 49).ok, false);
});

test("追溯吊销：知悉时间收到证书在 60 吊销，已排班次自动预警", () => {
  const matcher = makeMatcher();
  assert.equal(matcher.scheduleShift(shift("S1", 80, 95), 0).ok, true);
  assert.equal(matcher.scheduleShift(shift("S2", 20, 50), 0).ok, true);

  const report = matcher.recordRevocation("cert-1", 60, 200);
  assert.equal(report.certId, "cert-1");
  assert.equal(report.revokedAt, 60);
  assert.equal(report.knownAt, 200);
  assert.equal(report.violations.length, 1);
  assert.equal(report.violations[0].shift.id, "S1");
  assert.deepEqual(report.violations[0].missing, [{ start: 80, end: 95 }]);
});

test("追溯吊销：吊销点之后的有效区间被截断", () => {
  const matcher = makeMatcher();
  matcher.recordRevocation("cert-1", 60, 200);
  assert.deepEqual(matcher.effectiveValidity("cert-1", 200), { start: 10, end: 60 });
  // 知悉时间早于吊销通知时，仍按原有效期判定
  assert.deepEqual(matcher.effectiveValidity("cert-1", 199), { start: 10, end: 100 });
});

test("吊销与复审叠加：吊销截断优先（取最小 end）", () => {
  const matcher = makeMatcher();
  matcher.recordReview("cert-1", 150, 50);
  matcher.recordRevocation("cert-1", 60, 200);
  assert.deepEqual(matcher.effectiveValidity("cert-1", 200), { start: 10, end: 60 });
});

test("吊销与复审叠加：与事件登记顺序无关", () => {
  const matcher = makeMatcher();
  matcher.recordRevocation("cert-1", 60, 200);
  matcher.recordReview("cert-1", 150, 210);
  assert.deepEqual(matcher.effectiveValidity("cert-1", 210), { start: 10, end: 60 });
  const result = matcher.validateShift(shift("S1", 80, 120), 210);
  assert.equal(result.ok, false);
  assert.deepEqual(result.missing, [{ start: 80, end: 120 }]);
});

test("无对应技能证书时整段缺失", () => {
  const matcher = makeMatcher();
  const result = matcher.validateShift(
    { id: "S1", employeeId: "emp-1", skill: "welding", start: 20, end: 30 },
    0,
  );
  assert.equal(result.ok, false);
  assert.deepEqual(result.missing, [{ start: 20, end: 30 }]);
});
