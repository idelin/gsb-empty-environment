import { pairKey, type BitemporalRuleStore } from "./ruleStore.ts";
import type {
  ComplianceReport,
  LogicalTime,
  Prescription,
  RiskAuditReport,
  RiskFinding,
  RuleHit,
} from "./types.ts";

/** 收集处方中药品对在指定双时态坐标下命中的规则 */
function collectHits(
  store: BitemporalRuleStore,
  prescription: Prescription,
  validTime: LogicalTime,
  knownAt: LogicalTime,
): RuleHit[] {
  const pairs = new Map<string, { drugA: string; drugB: string }>();
  const drugs = prescription.drugs;
  for (let i = 0; i < drugs.length; i++) {
    for (let j = i + 1; j < drugs.length; j++) {
      const a = drugs[i];
      const b = drugs[j];
      pairs.set(pairKey(a, b), { drugA: a, drugB: b });
    }
  }

  const hits: RuleHit[] = [];
  for (const rule of store.rulesEffectiveAt(validTime, knownAt)) {
    const pair = pairs.get(pairKey(rule.drugA, rule.drugB));
    if (pair) {
      hits.push({ rule, drugA: pair.drugA, drugB: pair.drugB });
    }
  }
  return hits;
}

/**
 * 开立时合规性判定：
 * 以处方开立时间同时作为业务时间与知悉时间截点——
 * 即"以当时临床上生效、且当时已被知悉的规则"评判处方是否合规。
 * 事后更新的指南不影响历史处方的合规结论。
 */
export function checkCompliance(
  store: BitemporalRuleStore,
  prescription: Prescription,
): ComplianceReport {
  const violations = collectHits(
    store,
    prescription,
    prescription.issuedAt,
    prescription.issuedAt,
  );
  return {
    prescriptionId: prescription.prescriptionId,
    evaluatedAt: {
      validTime: prescription.issuedAt,
      knownAt: prescription.issuedAt,
    },
    compliant: violations.length === 0,
    violations,
  };
}

/**
 * 追溯风险审计：
 * 以审计时刻（通常为当前最新）生效且已知悉的规则，回看历史处方的
 * 药品组合，识别开立后才被指南揭示的配伍风险。
 * 药品相互作用是药理学事实：指南更新只是"揭示"了开立时未知悉的风险，
 * 因此审计的业务时间坐标取审计时刻而非开立时刻。
 */
export function auditRetrospectiveRisk(
  store: BitemporalRuleStore,
  prescription: Prescription,
  asOfKnownAt: LogicalTime,
): RiskAuditReport {
  const hits = collectHits(
    store,
    prescription,
    asOfKnownAt,
    asOfKnownAt,
  );
  const findings: RiskFinding[] = hits.map((hit) => ({
    ...hit,
    ruleKnownAt: hit.rule.recordedAt,
    guidelineVersion: hit.rule.guidelineVersion,
  }));
  return {
    prescriptionId: prescription.prescriptionId,
    evaluatedAt: {
      validTime: asOfKnownAt,
      knownAt: asOfKnownAt,
    },
    hasRetrospectiveRisk: findings.length > 0,
    findings,
  };
}
