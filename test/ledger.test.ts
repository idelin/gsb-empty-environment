import { test } from "node:test";
import assert from "node:assert/strict";
import { TemporalKpiLedger } from "../src/temporal-kpi-ledger.ts";

const closeTo = (actual: number, expected: number) =>
  assert.ok(Math.abs(actual - expected) < 1e-9, `期望 ${expected}，实际 ${actual}`);

function buildAcceptanceLedger() {
  const ledger = new TemporalKpiLedger({
    cycleStartDay: 1,
    cycleEndDay: 91,
    signingSecret: "hr-strategy-2026-q1",
  });
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.6, validFrom: 1, validTo: 60, actor: "alice", reason: "初始战略", recordedAtDay: 1 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.4, validFrom: 1, validTo: 60, actor: "alice", reason: "初始战略", recordedAtDay: 1 });
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 60, validTo: 91, actor: "alice", reason: "初始战略", recordedAtDay: 1 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 60, validTo: 91, actor: "alice", reason: "初始战略", recordedAtDay: 1 });
  for (let day = 1; day < 60; day++) {
    ledger.recordActual("A", day, 90);
    ledger.recordActual("B", day, 150);
  }
  for (let day = 60; day < 91; day++) {
    ledger.recordActual("A", day, 110);
    ledger.recordActual("B", day, 240);
  }
  return ledger;
}

test("验收：Q1 初始战略下季度末加权得分", () => {
  const ledger = buildAcceptanceLedger();
  const report = ledger.computeScore();
  assert.equal(report.asOfSeq, 4);
  assert.equal(report.warnings.length, 0);
  const expected =
    100 * ((59 / 90) * (0.6 * 0.9 + 0.4 * 0.75) + (31 / 90) * (0.4 * 1.1 + 0.6 * 1.2));
  closeTo(report.totalScore, expected);
  closeTo(report.totalScore, 95.02222222222223);
});

test("验收：第45天追溯调整为全季度 A=0.4/B=0.6 后的得分与分差", () => {
  const ledger = buildAcceptanceLedger();
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });

  const diff = ledger.diffScores(4, 6);
  closeTo(diff.beforeScore, 95.02222222222223);
  closeTo(diff.afterScore, 93.05555555555556);
  closeTo(diff.delta, -1.9666666666666686);

  const kpiA = diff.perKpi.find((k) => k.kpiId === "A")!;
  const kpiB = diff.perKpi.find((k) => k.kpiId === "B")!;
  closeTo(kpiA.delta, -11.8);
  closeTo(kpiB.delta, 9.833333333333343);
});

test("验收：追溯版本覆盖全季度，历史区间被新版本接管", () => {
  const ledger = buildAcceptanceLedger();
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });

  const after = ledger.computeScore();
  const firstSegment = after.contributions.filter((c) => c.validFrom === 1 && c.validTo === 60);
  assert.deepEqual(
    firstSegment.map((c) => c.sourceSeq).sort(),
    [5, 6],
    "区间 [1,60) 应由追溯版本 v5/v6 接管",
  );
});

test("验收：版本影响记录保留责任人签名且可校验", () => {
  const ledger = buildAcceptanceLedger();
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });

  const diff = ledger.diffScores(4, 6);
  assert.equal(diff.impactingVersions.length, 2);
  for (const v of diff.impactingVersions) {
    assert.equal(v.actor, "bob");
    assert.equal(v.recordedAtDay, 45);
    assert.match(v.signature, /^[0-9a-f]{64}$/);
    assert.ok(ledger.verifyVersion(v));
  }
});

test("签名防篡改：修改版本字段后校验失败", () => {
  const ledger = buildAcceptanceLedger();
  const v = ledger.listVersions()[0];
  assert.ok(ledger.verifyVersion(v));
  assert.ok(!ledger.verifyVersion({ ...v, weight: 0.9 }));
  assert.ok(!ledger.verifyVersion({ ...v, actor: "mallory" }));
});

test("历史快照推演：computeScore(asOfSeq) 忽略后续版本", () => {
  const ledger = buildAcceptanceLedger();
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.4, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });
  ledger.recordVersion({ kpiId: "B", targetValue: 200, weight: 0.6, validFrom: 1, validTo: 91, actor: "bob", reason: "战略转移", recordedAtDay: 45 });

  const snapshot = ledger.computeScore(4);
  closeTo(snapshot.totalScore, 95.02222222222223);

  const early = ledger.computeScore(2);
  assert.equal(early.asOfSeq, 2);
  closeTo(early.totalScore, 100 * (59 / 90) * (0.6 * 0.9 + 0.4 * 0.75));
  assert.ok(early.warnings.some((w) => w.includes("[60, 91)") && w.includes("无生效版本")));
});

test("区间切分：版本边界将周期切分为加权分段", () => {
  const ledger = new TemporalKpiLedger({ cycleStartDay: 1, cycleEndDay: 91 });
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 1, validFrom: 1, validTo: 31, actor: "a", reason: "r", recordedAtDay: 1 });
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 1, validFrom: 31, validTo: 91, actor: "a", reason: "r", recordedAtDay: 1 });
  for (let day = 1; day < 31; day++) ledger.recordActual("A", day, 50);
  for (let day = 31; day < 91; day++) ledger.recordActual("A", day, 100);

  const report = ledger.computeScore();
  assert.equal(report.contributions.length, 2);
  closeTo(report.contributions[0].segmentFraction, 30 / 90);
  closeTo(report.contributions[1].segmentFraction, 60 / 90);
  closeTo(report.totalScore, 100 * (30 / 90 * 0.5 + 60 / 90 * 1.0));
});

test("参数校验：非法权重/区间/目标值抛错", () => {
  const ledger = new TemporalKpiLedger({ cycleStartDay: 1, cycleEndDay: 91 });
  const base = { kpiId: "A", targetValue: 100, weight: 0.5, validFrom: 1, validTo: 91, actor: "a", reason: "r", recordedAtDay: 1 };
  assert.throws(() => ledger.recordVersion({ ...base, weight: 1.2 }), /权重/);
  assert.throws(() => ledger.recordVersion({ ...base, weight: -0.1 }), /权重/);
  assert.throws(() => ledger.recordVersion({ ...base, targetValue: 0 }), /目标值/);
  assert.throws(() => ledger.recordVersion({ ...base, validFrom: 50, validTo: 50 }), /有效区间/);
  assert.throws(() => ledger.recordVersion({ ...base, validTo: 92 }), /超出考评周期/);
  assert.throws(() => ledger.recordVersion({ ...base, actor: "" }), /责任人/);
  assert.throws(() => ledger.recordActual("A", 91, 1), /超出考评周期/);
});

test("权重合计偏离 1 时产生警告", () => {
  const ledger = new TemporalKpiLedger({ cycleStartDay: 1, cycleEndDay: 91 });
  ledger.recordVersion({ kpiId: "A", targetValue: 100, weight: 0.5, validFrom: 1, validTo: 91, actor: "a", reason: "r", recordedAtDay: 1 });
  ledger.recordActual("A", 10, 100);
  const report = ledger.computeScore();
  assert.ok(report.warnings.some((w) => w.includes("权重合计")));
});
