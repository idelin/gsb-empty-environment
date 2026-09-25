import { randomUUID } from 'node:crypto';
import { evaluate } from './evaluate.ts';
import { summarizePolicy } from './rule.ts';
import type { Policy, RuleSummary } from './rule.ts';
import { snapshotHash } from './snapshot.ts';
import type { AttributeSnapshot } from './snapshot.ts';
import type { EvaluationTrace, Outcome } from './trace.ts';

/**
 * The persisted evidence bundle: rule summary + attribute snapshot +
 * evaluation trace. Everything an offline verifier needs; nothing live.
 */
export interface EvidenceRecord {
  recordId: string;
  createdAt: string;
  ruleSummary: RuleSummary;
  snapshot: AttributeSnapshot;
  snapshotHash: string;
  trace: EvaluationTrace;
  conclusion: Outcome;
}

export function createEvidenceRecord(
  policy: Policy,
  snapshot: AttributeSnapshot,
  recordId: string = randomUUID(),
): EvidenceRecord {
  const { outcome, trace } = evaluate(policy.rule, snapshot);
  return {
    recordId,
    createdAt: new Date().toISOString(),
    ruleSummary: summarizePolicy(policy),
    snapshot,
    snapshotHash: snapshotHash(snapshot),
    trace,
    conclusion: outcome,
  };
}
