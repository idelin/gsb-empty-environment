import type { Decision, FileMeta, Policy, Rule } from './types.ts';
import { decisionEquals, effectiveAction, type EffectiveAction } from './types.ts';
import { summarizeCondition, summaryMayMatchFile, type ConstraintSummary } from './conditions.ts';
import { interpretFile } from './interpreter.ts';

export interface PolicyDiff {
  added: Rule[];
  removed: Rule[];
  modified: { before: Rule; after: Rule }[];
  defaultChanged: boolean;
}

function ruleSignature(rule: Rule): string {
  return JSON.stringify({
    priority: rule.priority,
    action: rule.action,
    when: rule.when,
  });
}

export function diffPolicies(oldPolicy: Policy, newPolicy: Policy): PolicyDiff {
  const oldById = new Map(oldPolicy.rules.map((r) => [r.id, r]));
  const newById = new Map(newPolicy.rules.map((r) => [r.id, r]));
  const added: Rule[] = [];
  const removed: Rule[] = [];
  const modified: { before: Rule; after: Rule }[] = [];

  for (const rule of newPolicy.rules) {
    const before = oldById.get(rule.id);
    if (before === undefined) added.push(rule);
    else if (ruleSignature(before) !== ruleSignature(rule)) {
      modified.push({ before, after: rule });
    }
  }
  for (const rule of oldPolicy.rules) {
    if (!newById.has(rule.id)) removed.push(rule);
  }
  return {
    added,
    removed,
    modified,
    defaultChanged: oldPolicy.defaultAction !== newPolicy.defaultAction,
  };
}

/**
 * Select the files whose decision could change under the new policy.
 *
 * A file is affected when:
 *  - its previous decision depended on a changed rule (it matched the rule,
 *    or the rule was among its pending-unknown rules), or
 *  - its previous decision references a rule that no longer exists in the
 *    new policy (a corrupt or stale snapshot), or
 *  - it may match the new version of an added/modified rule, or
 *  - it may have matched the old version of a removed/modified rule.
 *
 * Matching here is a conservative pre-filter over constraint summaries, so
 * files with unknown metadata are kept rather than optimistically skipped.
 * If the policy default changed, every file is potentially affected.
 */
export function selectAffectedFiles(
  files: readonly FileMeta[],
  oldDecisions: ReadonlyMap<string, Decision>,
  diff: PolicyDiff,
  newPolicy: Policy,
): Set<string> {
  const changedRuleIds = new Set<string>([
    ...diff.added.map((r) => r.id),
    ...diff.removed.map((r) => r.id),
    ...diff.modified.map((m) => m.after.id),
  ]);
  const newRuleIds = new Set(newPolicy.rules.map((r) => r.id));

  const probes: ConstraintSummary[] = [
    ...diff.added.map((r) => summarizeCondition(r.when)),
    ...diff.removed.map((r) => summarizeCondition(r.when)),
    ...diff.modified.flatMap((m) => [
      summarizeCondition(m.before.when),
      summarizeCondition(m.after.when),
    ]),
  ];

  const affected = new Set<string>();
  for (const file of files) {
    if (diff.defaultChanged) {
      affected.add(file.path);
      continue;
    }
    const decision = oldDecisions.get(file.path);
    if (
      decision !== undefined &&
      (decisionDependsOn(decision, changedRuleIds) || decisionReferencesUnknownRules(decision, newRuleIds))
    ) {
      affected.add(file.path);
      continue;
    }
    if (probes.some((summary) => summaryMayMatchFile(summary, file))) {
      affected.add(file.path);
    }
  }
  return affected;
}

function decisionReferencesUnknownRules(
  decision: Decision,
  newRuleIds: ReadonlySet<string>,
): boolean {
  if (decision.kind === 'decided') return !newRuleIds.has(decision.ruleId);
  if (decision.kind === 'unknown') {
    return decision.pendingRuleIds.some((id) => !newRuleIds.has(id));
  }
  return false;
}

function decisionDependsOn(decision: Decision, changedRuleIds: ReadonlySet<string>): boolean {
  if (decision.kind === 'decided') return changedRuleIds.has(decision.ruleId);
  if (decision.kind === 'unknown') {
    return decision.pendingRuleIds.some((id) => changedRuleIds.has(id));
  }
  return false;
}

export interface UpdatePlan {
  /** Files that were re-evaluated. */
  affectedFiles: string[];
  /** Files transitioning into an effective 'include' (new downloads). */
  downloads: string[];
  /** Files leaving an effective 'include' (revocations). */
  revocations: string[];
  /** Files whose decision is now 'unknown' and were not before. */
  becameUnknown: string[];
  /** Files that recovered a definite decision from 'unknown'. */
  resolved: string[];
}

/** Derive a download/revocation plan from two decision snapshots. */
export function derivePlan(
  oldDecisions: ReadonlyMap<string, Decision>,
  newDecisions: ReadonlyMap<string, Decision>,
  candidatePaths: Iterable<string>,
): UpdatePlan {
  const downloads: string[] = [];
  const revocations: string[] = [];
  const becameUnknown: string[] = [];
  const resolved: string[] = [];

  for (const path of candidatePaths) {
    const before = oldDecisions.get(path);
    const after = newDecisions.get(path);
    if (before === undefined || after === undefined) continue;
    if (decisionEquals(before, after)) continue;
    const from: EffectiveAction = effectiveAction(before);
    const to: EffectiveAction = effectiveAction(after);
    if (to === 'include' && from !== 'include') downloads.push(path);
    if (from === 'include' && to !== 'include') revocations.push(path);
    if (to === 'unknown' && from !== 'unknown') becameUnknown.push(path);
    if (from === 'unknown' && to !== 'unknown') resolved.push(path);
  }

  const sortAll = (xs: string[]) => xs.sort();
  return {
    affectedFiles: sortAll([...candidatePaths]),
    downloads: sortAll(downloads),
    revocations: sortAll(revocations),
    becameUnknown: sortAll(becameUnknown),
    resolved: sortAll(resolved),
  };
}

export interface IncrementalResult {
  plan: UpdatePlan;
  /** Decisions after the update: old decisions merged with recomputed ones. */
  decisions: Map<string, Decision>;
}

/**
 * Incremental policy update: recompute only the possibly affected files and
 * merge their new decisions into the previous snapshot.
 */
export function incrementalUpdate(
  oldPolicy: Policy,
  newPolicy: Policy,
  files: readonly FileMeta[],
  oldDecisions: ReadonlyMap<string, Decision>,
): IncrementalResult {
  const diff = diffPolicies(oldPolicy, newPolicy);
  const affected = selectAffectedFiles(files, oldDecisions, diff, newPolicy);
  const decisions = new Map(oldDecisions);
  for (const file of files) {
    if (affected.has(file.path)) decisions.set(file.path, interpretFile(newPolicy, file));
  }
  const plan = derivePlan(oldDecisions, decisions, affected);
  return { plan, decisions };
}
