import { BitemporalRuleStore } from "../src/ruleStore.ts";
import { auditRetrospectiveRisk, checkCompliance } from "../src/checker.ts";
import { Severity, type Prescription } from "../src/types.ts";

// ---- 验收场景时间线 ----
// t=10  处方 P 开立，含甲药 + 乙药；知识库此时无甲乙禁忌
// t=20  指南 v2 发布并录入：甲乙联用列为"慎用"
// t=25  查询开方合规性 + 生成风险审计报告

const store = new BitemporalRuleStore();

const prescription: Prescription = {
  prescriptionId: "P",
  issuedAt: 10,
  drugs: ["甲药", "乙药"],
};

// t=20：指南更新，录入知识库（业务生效时间 = 知悉时间 = 20）
store.record(
  {
    ruleId: "R-甲乙",
    drugA: "甲药",
    drugB: "乙药",
    severity: Severity.CAUTION,
    guidelineVersion: "指南v2",
    note: "联用可能增加不良反应风险",
  },
  20,
  20,
);

// t=25：开方合规性判定（锚定开立时刻 t=10 的临床生效规则与知悉状态）
const compliance = checkCompliance(store, prescription);
console.log("=== 开方合规性判定（查询于知悉时间 25） ===");
console.log(`处方 ${compliance.prescriptionId}（开立于 t=${prescription.issuedAt}）`);
console.log(
  `判定坐标：业务时间=${compliance.evaluatedAt.validTime}，知悉时间=${compliance.evaluatedAt.knownAt}`,
);
console.log(`结论：${compliance.compliant ? "合格" : "不合格"}`);

// t=25：追溯风险审计（用最新规则回看历史处方）
const audit = auditRetrospectiveRisk(store, prescription, 25);
console.log("\n=== 追溯风险审计报告（知悉时间 25） ===");
console.log(`存在追溯风险：${audit.hasRetrospectiveRisk ? "是" : "否"}`);
for (const finding of audit.findings) {
  console.log(
    `- [${finding.rule.severity}] ${finding.drugA} + ${finding.drugB}` +
      `（${finding.guidelineVersion}，规则知悉于 t=${finding.ruleKnownAt}，晚于开方 t=${prescription.issuedAt}）`,
  );
  if (finding.rule.note) {
    console.log(`  说明：${finding.rule.note}`);
  }
}
