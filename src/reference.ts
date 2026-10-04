import type { Decision, FileMeta, Policy } from './types.ts';
import { decisionEquals } from './types.ts';
import { fullScan } from './interpreter.ts';
import { derivePlan, incrementalUpdate, type UpdatePlan } from './incremental.ts';

export interface CrossCheckReport {
  ok: boolean;
  /** Paths where the incremental decision differs from the full scan. */
  decisionMismatches: { path: string; incremental: Decision; reference: Decision }[];
  /** Differences between the incremental plan and the reference plan. */
  planMismatches: string[];
}

function planFieldDiffs(name: string, a: readonly string[], b: readonly string[]): string[] {
  const diffs: string[] = [];
  const setA = new Set(a);
  const setB = new Set(b);
  for (const x of setA) if (!setB.has(x)) diffs.push(`${name}: "${x}" only in incremental plan`);
  for (const x of setB) if (!setA.has(x)) diffs.push(`${name}: "${x}" only in reference plan`);
  return diffs;
}

export function plansEqual(a: UpdatePlan, b: UpdatePlan): string[] {
  return [
    ...planFieldDiffs('downloads', a.downloads, b.downloads),
    ...planFieldDiffs('revocations', a.revocations, b.revocations),
    ...planFieldDiffs('becameUnknown', a.becameUnknown, b.becameUnknown),
    ...planFieldDiffs('resolved', a.resolved, b.resolved),
  ];
}

/**
 * Cross-check an incremental policy update against a full-scan reference.
 *
 * The reference re-interprets every file under the new policy and derives
 * the plan from the old snapshot, so any file the incremental pass wrongly
 * skipped (missing tags, boundary sizes, negated conditions, ...) shows up
 * as a mismatch.
 */
export function crossCheck(
  oldPolicy: Policy,
  newPolicy: Policy,
  files: readonly FileMeta[],
  oldDecisions: ReadonlyMap<string, Decision>,
): CrossCheckReport {
  const incremental = incrementalUpdate(oldPolicy, newPolicy, files, oldDecisions);
  const referenceDecisions = fullScan(newPolicy, files);
  const referencePlan = derivePlan(
    oldDecisions,
    referenceDecisions,
    files.map((f) => f.path),
  );

  const decisionMismatches: CrossCheckReport['decisionMismatches'] = [];
  for (const file of files) {
    const inc = incremental.decisions.get(file.path);
    const ref = referenceDecisions.get(file.path);
    if (inc === undefined || ref === undefined) {
      decisionMismatches.push({
        path: file.path,
        incremental: inc ?? ({ kind: 'default', action: newPolicy.defaultAction } as Decision),
        reference: ref ?? ({ kind: 'default', action: newPolicy.defaultAction } as Decision),
      });
    } else if (!decisionEquals(inc, ref)) {
      decisionMismatches.push({ path: file.path, incremental: inc, reference: ref });
    }
  }

  const planMismatches = plansEqual(incremental.plan, referencePlan);
  return {
    ok: decisionMismatches.length === 0 && planMismatches.length === 0,
    decisionMismatches,
    planMismatches,
  };
}
