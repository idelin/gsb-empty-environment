import type { Policy, Rule } from './types.ts';
import {
  summarizeCondition,
  summariesOverlap,
  summaryImplies,
  type ConstraintSummary,
} from './conditions.ts';

export type ConflictKind =
  | 'unsatisfiable-rule' // condition can never hold
  | 'ambiguous' // same priority, overlapping, different actions
  | 'shadowed' // unreachable: a higher-priority rule covers it, different action
  | 'redundant' // unreachable: a higher-priority rule covers it, same action
  | 'overlap'; // overlapping rules with different actions, resolved by priority

export interface Conflict {
  kind: ConflictKind;
  ruleIds: string[];
  message: string;
}

export interface AnalyzedRule {
  rule: Rule;
  summary: ConstraintSummary;
}

/** Rules sorted for evaluation: priority desc, then id asc for determinism. */
export function sortedRules(policy: Policy): Rule[] {
  return [...policy.rules].sort((a, b) =>
    b.priority !== a.priority ? b.priority - a.priority : a.id < b.id ? -1 : a.id > b.id ? 1 : 0,
  );
}

export function analyzePolicy(policy: Policy): AnalyzedRule[] {
  return sortedRules(policy).map((rule) => ({
    rule,
    summary: summarizeCondition(rule.when),
  }));
}

/**
 * Static conflict check. Runs over rule pairs without touching any file.
 * Relations between conditions are conservative: when overlap cannot be
 * disproved, the pair is reported rather than silently ignored.
 */
export function checkConflicts(policy: Policy): Conflict[] {
  const analyzed = analyzePolicy(policy);
  const conflicts: Conflict[] = [];

  for (const { rule, summary } of analyzed) {
    if (summary.unsatisfiable) {
      conflicts.push({
        kind: 'unsatisfiable-rule',
        ruleIds: [rule.id],
        message: `rule "${rule.id}" can never match: its condition is unsatisfiable`,
      });
    }
  }

  for (let i = 0; i < analyzed.length; i++) {
    for (let j = i + 1; j < analyzed.length; j++) {
      const hi = analyzed[i]; // higher or equal priority (sorted)
      const lo = analyzed[j];
      if (hi.summary.unsatisfiable || lo.summary.unsatisfiable) continue;
      if (!summariesOverlap(hi.summary, lo.summary)) continue;

      if (hi.rule.priority === lo.rule.priority) {
        if (hi.rule.action !== lo.rule.action) {
          conflicts.push({
            kind: 'ambiguous',
            ruleIds: [hi.rule.id, lo.rule.id],
            message:
              `rules "${hi.rule.id}" and "${lo.rule.id}" share priority ${hi.rule.priority} ` +
              `and may overlap but select different actions`,
          });
        }
        continue;
      }

      const loCovered = summaryImplies(lo.summary, hi.summary);
      if (loCovered) {
        conflicts.push({
          kind: hi.rule.action === lo.rule.action ? 'redundant' : 'shadowed',
          ruleIds: [hi.rule.id, lo.rule.id],
          message:
            `rule "${lo.rule.id}" is unreachable: every file it matches is already ` +
            `covered by higher-priority rule "${hi.rule.id}"`,
        });
      } else if (hi.rule.action !== lo.rule.action) {
        conflicts.push({
          kind: 'overlap',
          ruleIds: [hi.rule.id, lo.rule.id],
          message:
            `rules "${hi.rule.id}" (priority ${hi.rule.priority}) and "${lo.rule.id}" ` +
            `(priority ${lo.rule.priority}) may overlap with different actions; ` +
            `"${hi.rule.id}" wins on shared files`,
        });
      }
    }
  }

  return conflicts;
}
