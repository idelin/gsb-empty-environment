import { evaluate, MissingAttributeError } from './evaluate.ts';
import type { EvidenceRecord } from './evidence.ts';
import { summarizePolicy } from './rule.ts';
import type { Policy, RuleSummary } from './rule.ts';
import { isSealed, snapshotHash } from './snapshot.ts';
import type { EvaluationTrace, Outcome, ReadRecord } from './trace.ts';

export type Divergence =
  | { kind: 'rule-summary-mismatch'; expected: RuleSummary; actual: RuleSummary }
  | { kind: 'snapshot-tampered'; expected: string; actual: string }
  | { kind: 'missing-evidence'; path: string; detail: string }
  | { kind: 'trace-mismatch'; path: string; field: string; expected: unknown; actual: unknown }
  | { kind: 'read-set-mismatch'; index: number; expected: ReadRecord | null; actual: ReadRecord | null }
  | { kind: 'sealed-claim-rejected'; attr: string; claimed: number }
  | { kind: 'conclusion-mismatch'; expected: Outcome; actual: Outcome };

export type VerificationResult =
  | { status: 'verified'; conclusion: boolean }
  | { status: 'unverifiable'; reason: string; sealedAttributes: string[] }
  | { status: 'rejected'; divergence: Divergence };

function rejected(divergence: Divergence): VerificationResult {
  return { status: 'rejected', divergence };
}

function readsEqual(a: ReadRecord, b: ReadRecord): boolean {
  return a.attr === b.attr && a.value === b.value;
}

/**
 * Structural comparison of the replayed (trusted) trace against the stored
 * (untrusted) trace. Walks every branch in evaluation order and reports the
 * FIRST divergence; never settles for recomputing the final boolean.
 */
function compareTraces(expected: EvaluationTrace, actual: EvaluationTrace): Divergence | null {
  if (expected.root !== actual.root) {
    return { kind: 'trace-mismatch', path: '$', field: 'root', expected: expected.root, actual: actual.root };
  }

  const seen = new Set<string>();
  const stack: Array<{ hash: string; path: string }> = [{ hash: expected.root, path: '$' }];

  while (stack.length > 0) {
    const { hash, path } = stack.pop() as { hash: string; path: string };
    if (seen.has(hash)) continue;
    seen.add(hash);

    const exp = expected.subproofs[hash];
    const act = actual.subproofs[hash];
    if (!act) {
      return { kind: 'missing-evidence', path, detail: `subproof ${hash} is absent from the stored trace` };
    }
    if (act.ruleHash !== exp.ruleHash) {
      return { kind: 'trace-mismatch', path, field: 'ruleHash', expected: exp.ruleHash, actual: act.ruleHash };
    }
    if (act.outcome !== exp.outcome) {
      return { kind: 'trace-mismatch', path, field: 'outcome', expected: exp.outcome, actual: act.outcome };
    }

    const readCount = Math.max(exp.reads.length, act.reads.length);
    for (let i = 0; i < readCount; i++) {
      const e = exp.reads[i];
      const a = act.reads[i];
      if (!e || !a || !readsEqual(e, a)) {
        return { kind: 'trace-mismatch', path, field: `reads[${i}]`, expected: e ?? null, actual: a ?? null };
      }
    }

    const childCount = Math.max(exp.children.length, act.children.length);
    for (let i = 0; i < childCount; i++) {
      if (exp.children[i] !== act.children[i]) {
        return {
          kind: 'trace-mismatch',
          path,
          field: `children[${i}]`,
          expected: exp.children[i] ?? null,
          actual: act.children[i] ?? null,
        };
      }
    }

    const skipCount = Math.max(exp.skipped.length, act.skipped.length);
    for (let i = 0; i < skipCount; i++) {
      if (exp.skipped[i] !== act.skipped[i]) {
        return {
          kind: 'trace-mismatch',
          path,
          field: `skipped[${i}]`,
          expected: exp.skipped[i] ?? null,
          actual: act.skipped[i] ?? null,
        };
      }
    }

    for (let i = exp.children.length - 1; i >= 0; i--) {
      stack.push({ hash: exp.children[i], path: `${path}/child[${i}]` });
    }
  }

  for (const hash of Object.keys(actual.subproofs)) {
    if (!expected.subproofs[hash]) {
      return { kind: 'trace-mismatch', path: '$', field: `subproofs[${hash}]`, expected: null, actual: 'unexpected extra subproof' };
    }
  }

  const total = Math.max(expected.reads.length, actual.reads.length);
  for (let i = 0; i < total; i++) {
    const e = expected.reads[i] ?? null;
    const a = actual.reads[i] ?? null;
    if (!e || !a || !readsEqual(e, a)) {
      return { kind: 'read-set-mismatch', index: i, expected: e, actual: a };
    }
  }

  return null;
}

/**
 * Offline replay verification. Uses ONLY the evidence record and the local
 * policy copy — live attribute sources are never consulted, so later
 * corrections to real-time attributes cannot change a historical verdict.
 */
export function verifyRecord(record: EvidenceRecord, policy: Policy): VerificationResult {
  // 1. The stored rule summary must match the verifier's own policy copy,
  //    field by field (a swapped summary or bumped version is rejected).
  const summary = summarizePolicy(policy);
  if (
    summary.hash !== record.ruleSummary.hash ||
    summary.id !== record.ruleSummary.id ||
    summary.version !== record.ruleSummary.version
  ) {
    return rejected({ kind: 'rule-summary-mismatch', expected: record.ruleSummary, actual: summary });
  }

  // 2. Snapshot integrity.
  const actualSnapshotHash = snapshotHash(record.snapshot);
  if (actualSnapshotHash !== record.snapshotHash) {
    return rejected({ kind: 'snapshot-tampered', expected: record.snapshotHash, actual: actualSnapshotHash });
  }

  // 3. Source claims about sealed attributes are never accepted: any concrete
  //    value claimed for a sealed field voids the record.
  for (const read of record.trace.reads) {
    const snapValue = record.snapshot[read.attr];
    if (snapValue !== undefined && isSealed(snapValue) && read.value !== 'sealed') {
      return rejected({ kind: 'sealed-claim-rejected', attr: read.attr, claimed: read.value });
    }
  }

  // 4. Re-run the evaluation offline against the historical snapshot.
  let replay;
  try {
    replay = evaluate(policy.rule, record.snapshot);
  } catch (err) {
    if (err instanceof MissingAttributeError) {
      return rejected({ kind: 'missing-evidence', path: '$', detail: err.message });
    }
    throw err;
  }

  // 5. Compare every branch and the actual read set, not just the final bit.
  const divergence = compareTraces(replay.trace, record.trace);
  if (divergence) return rejected(divergence);

  // 6. The claimed conclusion must equal the replayed outcome.
  if (replay.outcome !== record.conclusion) {
    return rejected({ kind: 'conclusion-mismatch', expected: replay.outcome, actual: record.conclusion });
  }

  // 7. A conclusion blocked by sealed fields is marked unverifiable.
  if (replay.outcome === 'unknown') {
    const sealedAttributes = [...new Set(record.trace.reads.filter((r) => r.value === 'sealed').map((r) => r.attr))];
    return {
      status: 'unverifiable',
      reason: 'evaluation depends on sealed attribute(s); conclusion cannot be verified',
      sealedAttributes,
    };
  }

  return { status: 'verified', conclusion: replay.outcome === 'true' };
}
