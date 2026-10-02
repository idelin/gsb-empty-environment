import { BitemporalRuleStore } from './rule-store.ts';
import type {
  BitemporalRuleRecord,
  ComplianceReport,
  Prescription,
  RetroactiveRisk,
  RiskAuditReport,
  RuleHit,
} from './types.ts';

/** 处方药品两两组合（无序对） */
function drugPairs(drugs: readonly string[]): Array<[string, string]> {
  const pairs: Array<[string, string]> = [];
  for (let i = 0; i < drugs.length; i += 1) {
    for (let j = i + 1; j < drugs.length; j += 1) {
      pairs.push([drugs[i], drugs[j]]);
    }
  }
  return pairs;
}

function pairMatches(rule: BitemporalRuleRecord, a: string, b: string): boolean {
  return (rule.drugA === a && rule.drugB === b) || (rule.drugA === b && rule.drugB === a);
}

function findHits(
  prescription: Prescription,
  rules: readonly BitemporalRuleRecord[],
): RuleHit[] {
  const hits: RuleHit[] = [];
  for (const [a, b] of drugPairs(prescription.drugs)) {
    for (const rule of rules) {
      if (pairMatches(rule, a, b)) {
        hits.push({ rule, drugA: a, drugB: b });
      }
    }
  }
  return hits;
}

/**
 * 处方配伍禁忌判定器。
 * - 开方合规性：以开方时刻的知悉状态回看（默认 asOfKnowledge = 开方时间），
 *   回答“按当时已知的医学规则，这张处方开得合不合规”。
 * - 追溯风险审计：以最新知悉状态回看同一历史处方，
 *   回答“按今天已知的医学规则，这张处方是否暴露出新风险”。
 */
export class PrescriptionChecker {
  private readonly store: BitemporalRuleStore;

  constructor(store: BitemporalRuleStore) {
    this.store = store;
  }

  /** 开方合规性判定：asOfKnowledge 缺省为开方时间（即“开方时是否合规”） */
  checkCompliance(prescription: Prescription, asOfKnowledge?: number): ComplianceReport {
    const knowledge = asOfKnowledge ?? prescription.issuedAt;
    const rules = this.store.rulesInEffect(knowledge, prescription.issuedAt);
    const hits = findHits(prescription, rules);
    const violations = hits.filter((h) => h.rule.severity === 'contraindicated');
    const warnings = hits.filter((h) => h.rule.severity === 'caution');
    return {
      prescriptionId: prescription.id,
      issuedAt: prescription.issuedAt,
      asOfKnowledge: knowledge,
      compliant: violations.length === 0,
      violations,
      warnings,
    };
  }

  /**
   * 追溯风险审计：比较“开方时知悉”与“当前知悉”两个时态切片。
   * 当前切片中出现、而开方时切片中不存在的命中，即为追溯风险。
   */
  auditRetroactiveRisk(prescription: Prescription, asOfKnowledge?: number): RiskAuditReport {
    const knowledge = asOfKnowledge ?? this.store.latestKnowledgeTime();
    const atIssue = this.checkCompliance(prescription, prescription.issuedAt);
    const nowRules = this.store.rulesInEffect(knowledge, prescription.issuedAt);
    const nowHits = findHits(prescription, nowRules);

    const knownAtIssue = new Set(
      [...atIssue.violations, ...atIssue.warnings].map((h) => h.rule.ruleId),
    );
    const retroactiveRisks: RetroactiveRisk[] = nowHits
      .filter((h) => !knownAtIssue.has(h.rule.ruleId))
      .map((h) => ({
        drugA: h.drugA,
        drugB: h.drugB,
        severity: h.rule.severity,
        guidelineVersion: h.rule.guidelineVersion,
        knownSince:
          nowRules.find((r) => r.ruleId === h.rule.ruleId)?.recordedFrom ?? knowledge,
        description: h.rule.description,
      }));

    return {
      prescriptionId: prescription.id,
      issuedAt: prescription.issuedAt,
      auditedAtKnowledge: knowledge,
      compliantAtIssue: atIssue.compliant,
      hasRetroactiveRisk: retroactiveRisks.length > 0,
      retroactiveRisks,
    };
  }
}
