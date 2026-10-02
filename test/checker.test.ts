import { test } from "node:test";
import assert from "node:assert/strict";

import { BitemporalRuleStore } from "../src/ruleStore.ts";
import { auditRetrospectiveRisk, checkCompliance } from "../src/checker.ts";
import { Severity, type Prescription } from "../src/types.ts";

/**
 * 验收场景时间线（逻辑时间）：
 *   t=10  处方 P 开立，含甲药 + 乙药；知识库此时无甲乙禁忌
 *   t=20  指南 v2 更新，甲乙列为"慎用"，录入知识库
 *   t=25  查询：开方合规性 = 合格；风险审计报告 = 标记追溯风险
 */
function buildAcceptanceScenario() {
  const store = new BitemporalRuleStore();
  const prescription: Prescription = {
    prescriptionId: "P",
    issuedAt: 10,
    drugs: ["甲药", "乙药"],
  };
  store.record(
    {
      ruleId: "R-甲乙",
      drugA: "甲药",
      drugB: "乙药",
      severity: Severity.CAUTION,
      guidelineVersion: "指南v2",
      note: "联用可能增加不良反应风险",
    },
    /* validFrom */ 20,
    /* recordedAt */ 20,
  );
  return { store, prescription };
}

test("验收：知悉25查询开方合规性为合格", () => {
  const { store, prescription } = buildAcceptanceScenario();
  // 合规判定锚定处方开立时刻（业务时间=知悉时间=10），与查询时刻无关
  const report = checkCompliance(store, prescription);
  assert.equal(report.compliant, true);
  assert.deepEqual(report.violations, []);
  assert.deepEqual(report.evaluatedAt, { validTime: 10, knownAt: 10 });
});

test("验收：知悉25风险审计报告标记追溯风险", () => {
  const { store, prescription } = buildAcceptanceScenario();
  const report = auditRetrospectiveRisk(store, prescription, 25);
  assert.equal(report.hasRetrospectiveRisk, true);
  assert.equal(report.findings.length, 1);
  const finding = report.findings[0];
  assert.equal(finding.rule.severity, Severity.CAUTION);
  assert.equal(finding.guidelineVersion, "指南v2");
  assert.equal(finding.ruleKnownAt, 20);
  assert.ok(finding.ruleKnownAt > prescription.issuedAt, "规则知悉晚于开方，属追溯风险");
});

test("验收：在指南更新前（知悉15）审计则无风险", () => {
  const { store, prescription } = buildAcceptanceScenario();
  const report = auditRetrospectiveRisk(store, prescription, 15);
  assert.equal(report.hasRetrospectiveRisk, false);
  assert.deepEqual(report.findings, []);
});

test("开立前已知悉的禁忌规则导致不合规", () => {
  const store = new BitemporalRuleStore();
  store.record(
    {
      ruleId: "R-丙丁",
      drugA: "丙药",
      drugB: "丁药",
      severity: Severity.CONTRAINDICATED,
      guidelineVersion: "指南v1",
    },
    5,
    5,
  );
  const report = checkCompliance(store, {
    prescriptionId: "Q",
    issuedAt: 10,
    drugs: ["丙药", "丁药", "戊药"],
  });
  assert.equal(report.compliant, false);
  assert.equal(report.violations.length, 1);
  assert.equal(report.violations[0].rule.severity, Severity.CONTRAINDICATED);
});

test("规则更正保留双时态历史：更正前的认知仍可查询", () => {
  const store = new BitemporalRuleStore();
  // t=5 录入：甲乙禁用（指南v1）
  store.record(
    {
      ruleId: "R-甲乙",
      drugA: "甲药",
      drugB: "乙药",
      severity: Severity.CONTRAINDICATED,
      guidelineVersion: "指南v1",
    },
    5,
    5,
  );
  // t=30 更正：指南v3 追溯性重评，认定甲乙自始至终仅为慎用
  // （知悉时间=30，业务生效时间仍追溯至 5）
  store.record(
    {
      ruleId: "R-甲乙",
      drugA: "甲药",
      drugB: "乙药",
      severity: Severity.CAUTION,
      guidelineVersion: "指南v3",
    },
    5,
    30,
  );

  // 以 t=20 的知悉回看：仍是禁用
  const asKnownAt20 = store.rulesEffectiveAt(20, 20);
  assert.equal(asKnownAt20.length, 1);
  assert.equal(asKnownAt20[0].severity, Severity.CONTRAINDICATED);

  // 以 t=40 的知悉回看同一业务时间：已降级为慎用
  const asKnownAt40 = store.rulesEffectiveAt(20, 40);
  assert.equal(asKnownAt40.length, 1);
  assert.equal(asKnownAt40[0].severity, Severity.CAUTION);

  // 处方在 t=20 开立：按当时知悉（禁用）判定不合规
  const compliance = checkCompliance(store, {
    prescriptionId: "R",
    issuedAt: 20,
    drugs: ["甲药", "乙药"],
  });
  assert.equal(compliance.compliant, false);
});

test("业务时间生效性：规则生效前的业务时间不命中", () => {
  const store = new BitemporalRuleStore();
  // 规则 t=10 录入，但指南业务生效日为 t=20
  store.record(
    {
      ruleId: "R-己庚",
      drugA: "己药",
      drugB: "庚药",
      severity: Severity.CAUTION,
      guidelineVersion: "指南v2",
    },
    20,
    10,
  );
  // 业务时间 15（规则已知悉但尚未生效）不命中
  assert.equal(store.rulesEffectiveAt(15, 30).length, 0);
  // 业务时间 25 命中
  assert.equal(store.rulesEffectiveAt(25, 30).length, 1);
});

test("药品对无序：甲乙与乙甲命中同一条规则", () => {
  const store = new BitemporalRuleStore();
  store.record(
    {
      ruleId: "R-甲乙",
      drugA: "甲药",
      drugB: "乙药",
      severity: Severity.CAUTION,
      guidelineVersion: "指南v2",
    },
    1,
    1,
  );
  const report = checkCompliance(store, {
    prescriptionId: "S",
    issuedAt: 5,
    drugs: ["乙药", "甲药"],
  });
  assert.equal(report.compliant, false);
});
