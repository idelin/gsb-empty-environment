import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { BitemporalRuleStore } from '../src/rule-store.ts';
import { PrescriptionChecker } from '../src/checker.ts';
import type { Prescription } from '../src/types.ts';

describe('验收边界：双时态判定主场景', () => {
  // 时间线：
  //  业务时间 10：处方 P 开立，含甲药、乙药
  //  知悉时间 10：规则库中无甲乙禁忌
  //  知悉时间 20：指南 v2.0 收录，甲乙列为“慎用”
  //  知悉时间 25：查询开方合规性 => 合格；风险审计 => 标记追溯风险
  function buildScenario() {
    const store = new BitemporalRuleStore();
    const checker = new PrescriptionChecker(store);
    const prescription: Prescription = { id: 'P', issuedAt: 10, drugs: ['甲药', '乙药'] };

    // 知悉 20：指南 v2.0 将甲乙列为慎用（规则自始生效，覆盖业务时间 10）
    store.recordRule(
      {
        ruleId: 'R-甲乙',
        drugA: '甲药',
        drugB: '乙药',
        severity: 'caution',
        guidelineVersion: 'v2.0',
        description: '甲药与乙药联用可能增强不良反应，建议慎用并监测。',
      },
      { recordedAt: 20, validFrom: 0 },
    );
    return { store, checker, prescription };
  }

  it('知悉 10 时查询：规则库无禁忌，开方合规', () => {
    const { checker, prescription } = buildScenario();
    const report = checker.checkCompliance(prescription, 10);
    assert.equal(report.compliant, true);
    assert.equal(report.violations.length, 0);
    assert.equal(report.warnings.length, 0);
  });

  it('知悉 25 查询开方合规性（回看开方时刻）：合格', () => {
    const { checker, prescription } = buildScenario();
    // 默认以开方时间的知悉状态判定“开方时是否合规”
    const report = checker.checkCompliance(prescription);
    assert.equal(report.asOfKnowledge, 10);
    assert.equal(report.compliant, true);
  });

  it('知悉 25 风险审计：标记追溯风险（甲乙慎用）', () => {
    const { checker, prescription } = buildScenario();
    const audit = checker.auditRetroactiveRisk(prescription, 25);
    assert.equal(audit.compliantAtIssue, true);
    assert.equal(audit.hasRetroactiveRisk, true);
    assert.equal(audit.retroactiveRisks.length, 1);
    const risk = audit.retroactiveRisks[0];
    assert.equal(risk.severity, 'caution');
    assert.equal(risk.guidelineVersion, 'v2.0');
    assert.equal(risk.knownSince, 20);
    assert.deepEqual([risk.drugA, risk.drugB].sort(), ['乙药', '甲药'].sort());
  });

  it('知悉 25 以当前知悉直接判定：出现慎用警示', () => {
    const { checker, prescription } = buildScenario();
    const report = checker.checkCompliance(prescription, 25);
    assert.equal(report.compliant, true); // 慎用不构成不合规
    assert.equal(report.warnings.length, 1);
  });
});

describe('双时态语义', () => {
  it('禁用级规则在开方前已知悉：判定不合规', () => {
    const store = new BitemporalRuleStore();
    store.recordRule(
      {
        ruleId: 'R-1',
        drugA: 'A',
        drugB: 'B',
        severity: 'contraindicated',
        guidelineVersion: 'v1.0',
        description: 'A 与 B 禁止联用。',
      },
      { recordedAt: 5 },
    );
    const checker = new PrescriptionChecker(store);
    const report = checker.checkCompliance({ id: 'P1', issuedAt: 10, drugs: ['A', 'B', 'C'] });
    assert.equal(report.compliant, false);
    assert.equal(report.violations.length, 1);
  });

  it('规则业务时间未覆盖开方时刻：不适用', () => {
    const store = new BitemporalRuleStore();
    store.recordRule(
      {
        ruleId: 'R-2',
        drugA: 'A',
        drugB: 'B',
        severity: 'contraindicated',
        guidelineVersion: 'v1.0',
        description: '仅对业务时间 50 起的处方生效。',
      },
      { recordedAt: 5, validFrom: 50 },
    );
    const checker = new PrescriptionChecker(store);
    const report = checker.checkCompliance({ id: 'P2', issuedAt: 10, drugs: ['A', 'B'] }, 100);
    assert.equal(report.compliant, true);
  });

  it('规则被撤销：历史知悉仍可见，最新知悉不可见', () => {
    const store = new BitemporalRuleStore();
    store.recordRule(
      {
        ruleId: 'R-3',
        drugA: 'A',
        drugB: 'B',
        severity: 'caution',
        guidelineVersion: 'v1.0',
        description: '旧指南曾列为慎用。',
      },
      { recordedAt: 5 },
    );
    store.retractRule('R-3', 30);
    const checker = new PrescriptionChecker(store);
    const p: Prescription = { id: 'P3', issuedAt: 10, drugs: ['A', 'B'] };
    // 知悉 20（撤销前）：可见慎用警示
    assert.equal(checker.checkCompliance(p, 20).warnings.length, 1);
    // 知悉 40（撤销后）：规则不再生效
    assert.equal(checker.checkCompliance(p, 40).warnings.length, 0);
  });

  it('多版本演进：同一规则升级后，审计采用最新版本', () => {
    const store = new BitemporalRuleStore();
    store.recordRule(
      {
        ruleId: 'R-4',
        drugA: 'A',
        drugB: 'B',
        severity: 'caution',
        guidelineVersion: 'v1.0',
        description: 'v1.0：慎用。',
      },
      { recordedAt: 5 },
    );
    store.recordRule(
      {
        ruleId: 'R-4',
        drugA: 'A',
        drugB: 'B',
        severity: 'contraindicated',
        guidelineVersion: 'v2.0',
        description: 'v2.0：升级为禁用。',
      },
      { recordedAt: 20 },
    );
    const checker = new PrescriptionChecker(store);
    const p: Prescription = { id: 'P4', issuedAt: 10, drugs: ['A', 'B'] };
    // 开方时（知悉 10）：仅 v1.0 慎用，合规但有警示
    const atIssue = checker.checkCompliance(p);
    assert.equal(atIssue.compliant, true);
    assert.equal(atIssue.warnings[0].rule.guidelineVersion, 'v1.0');
    // 最新知悉（25）：v2.0 禁用
    const now = checker.checkCompliance(p, 25);
    assert.equal(now.compliant, false);
    assert.equal(now.violations[0].rule.guidelineVersion, 'v2.0');
    // 审计：规则开方时已知悉（慎用），不计为新增追溯风险
    const audit = checker.auditRetroactiveRisk(p, 25);
    assert.equal(audit.hasRetroactiveRisk, false);
  });

  it('知悉时间回退被拒绝', () => {
    const store = new BitemporalRuleStore();
    const rule = {
      ruleId: 'R-5',
      drugA: 'A',
      drugB: 'B',
      severity: 'caution' as const,
      guidelineVersion: 'v1.0',
      description: 'x',
    };
    store.recordRule(rule, { recordedAt: 20 });
    assert.throws(() => store.recordRule(rule, { recordedAt: 10 }), /知悉时间回退/);
  });
});
