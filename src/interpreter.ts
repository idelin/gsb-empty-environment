import type { Decision, FileMeta, Policy } from './types.ts';
import { evalCondition } from './conditions.ts';
import { sortedRules } from './policy.ts';

/**
 * Interpret one file against a policy.
 *
 * Rules are tried from highest priority down. A rule that evaluates to
 * 'unknown' (e.g. the file's tags have not been fetched) does not select an
 * action, but it blocks lower-priority rules: the file's decision stays
 * 'unknown' instead of falling through to a definite exclude. Only when no
 * rule is true and none is unknown does the policy default apply.
 */
export function interpretFile(policy: Policy, file: FileMeta): Decision {
  const pending: string[] = [];
  for (const rule of sortedRules(policy)) {
    const result = evalCondition(rule.when, file);
    if (result === 'true') {
      if (pending.length > 0) {
        return { kind: 'unknown', pendingRuleIds: [...pending] };
      }
      return { kind: 'decided', action: rule.action, ruleId: rule.id };
    }
    if (result === 'unknown') pending.push(rule.id);
  }
  if (pending.length > 0) return { kind: 'unknown', pendingRuleIds: pending };
  return { kind: 'default', action: policy.defaultAction };
}

/** Full-scan reference: interpret every file from scratch. */
export function fullScan(policy: Policy, files: readonly FileMeta[]): Map<string, Decision> {
  const out = new Map<string, Decision>();
  for (const file of files) out.set(file.path, interpretFile(policy, file));
  return out;
}
