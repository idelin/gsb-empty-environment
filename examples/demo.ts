import { BitemporalRuleStore } from '../src/rule-store.ts';
import { PrescriptionChecker } from '../src/checker.ts';
import type { Prescription } from '../src/types.ts';

// ============ 验收边界场景复现 ============
// 业务时间 10：处方 P 开立，含甲药 + 乙药
// 知悉时间 10：规则库中无甲乙禁忌
// 知悉时间 20：指南 v2.0 收录，甲乙列为“慎用”
// 知悉时间 25：查询开方合规性 => 合格；风险审计 => 标记追溯风险

const store = new BitemporalRuleStore();
const checker = new PrescriptionChecker(store);

const prescription: Prescription = { id: 'P', issuedAt: 10, drugs: ['甲药', '乙药'] };

console.log('== 知悉时间 10：开方时刻判定 ==');
const atIssue = checker.checkCompliance(prescription, 10);
console.log(`处方 ${prescription.id}（业务时间 ${prescription.issuedAt} 开立，药品：${prescription.drugs.join('、')}）`);
console.log(`合规性：${atIssue.compliant ? '合格' : '不合格'}（当时规则库无甲乙禁忌）\n`);

console.log('== 知悉时间 20：指南 v2.0 收录新规则 ==');
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
console.log('已收录：甲药 + 乙药 => 慎用（指南 v2.0，自始生效）\n');

console.log('== 知悉时间 25：查询开方合规性 ==');
const compliance = checker.checkCompliance(prescription);
console.log(`开方合规性（按开方时知悉 ${compliance.asOfKnowledge} 判定）：${compliance.compliant ? '合格' : '不合格'}\n`);

console.log('== 知悉时间 25：追溯风险审计 ==');
const audit = checker.auditRetroactiveRisk(prescription, 25);
console.log(`开方时合规：${audit.compliantAtIssue ? '是' : '否'}`);
console.log(`追溯风险：${audit.hasRetroactiveRisk ? '有' : '无'}`);
for (const risk of audit.retroactiveRisks) {
  console.log(
    `  [追溯风险] ${risk.drugA} + ${risk.drugB} => ${risk.severity === 'caution' ? '慎用' : '禁用'}` +
      `（指南 ${risk.guidelineVersion}，知悉于 ${risk.knownSince}）：${risk.description}`,
  );
}
