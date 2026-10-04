import { evalCondition, isLiteralGlob, matchGlob } from './conditions';
import { Action, Condition, Decision, FileFacts, Range, Rule, Tri } from './types';

export interface RuleEval {
  rule: Rule;
  result: Tri;
}

/** Evaluate every rule against one file. */
export function evalRules(rules: Rule[], facts: FileFacts): RuleEval[] {
  return rules.map((rule) => ({ rule, result: evalCondition(rule.condition, facts) }));
}

/**
 * Pick a decision from per-rule evaluations.
 *
 * Semantics: let T be rules evaluating `true` and U rules evaluating
 * `unknown`. The file is decided only when the best `true` rule outranks
 * every `unknown` rule; otherwise the outcome stays `unknown` — unknown
 * conditions are never silently treated as exclusion.
 */
export function decide(evals: RuleEval[], defaultAction: Action): Decision {
  const trues = evals.filter((e) => e.result === true);
  const unknowns = evals.filter((e) => e.result === 'unknown');

  const bestTrue = bestByPriority(trues);
  const bestUnknown = bestByPriority(unknowns);

  if (bestTrue && (!bestUnknown || bestTrue.rule.priority > bestUnknown.rule.priority)) {
    return { kind: 'decided', action: bestTrue.rule.action, ruleId: bestTrue.rule.id };
  }
  if (bestUnknown) {
    const pending = unknowns
      .slice()
      .sort((a, b) => b.rule.priority - a.rule.priority || a.rule.id.localeCompare(b.rule.id))
      .map((e) => e.rule.id);
    return { kind: 'unknown', pendingRuleIds: pending };
  }
  return { kind: 'default', action: defaultAction };
}

function bestByPriority(evals: RuleEval[]): RuleEval | undefined {
  let best: RuleEval | undefined;
  for (const e of evals) {
    if (!best || e.rule.priority > best.rule.priority ||
        (e.rule.priority === best.rule.priority && e.rule.id < best.rule.id)) {
      best = e;
    }
  }
  return best;
}

/** Interpret a single file against a policy. */
export function interpret(rules: Rule[], facts: FileFacts, defaultAction: Action = 'exclude'): Decision {
  return decide(evalRules(rules, facts), defaultAction);
}

// ---------------------------------------------------------------------------
// Static conflict checking
// ---------------------------------------------------------------------------

export interface PolicyIssue {
  severity: 'error' | 'warning';
  kind: 'duplicate-id' | 'unsatisfiable' | 'ambiguous' | 'shadowed';
  ruleIds: string[];
  message: string;
}

function rangesOverlap(a: Range, b: Range): boolean {
  const lo = Math.max(a.min ?? -Infinity, b.min ?? -Infinity);
  const hi = Math.min(a.max ?? Infinity, b.max ?? Infinity);
  return lo <= hi;
}

/** True when range `inner` is fully contained in range `outer`. */
function rangeContains(outer: Range, inner: Range): boolean {
  if (outer.min !== undefined && (inner.min === undefined || inner.min < outer.min)) return false;
  if (outer.max !== undefined && (inner.max === undefined || inner.max > outer.max)) return false;
  return true;
}

function globsMayOverlap(a: string, b: string): boolean {
  const aLit = isLiteralGlob(a);
  const bLit = isLiteralGlob(b);
  if (aLit && bLit) return a === b;
  if (aLit) return matchGlob(b, a);
  if (bLit) return matchGlob(a, b);
  return true; // conservative: two non-literal globs may share a path
}

/** True when the space cut out by `glob` is disjoint from `notGlob`'s complement. */
function globExcludedByNot(glob: string, notGlob: string): boolean {
  if (isLiteralGlob(glob)) return matchGlob(notGlob, glob);
  return glob === notGlob; // conservative: identical glob => fully excluded
}

/**
 * Conservative overlap test: returns true if some concrete file could
 * satisfy both conditions. May over-approximate, never under-approximate.
 */
export function conditionsMayOverlap(a: Condition, b: Condition): boolean {
  if (a.size && b.size && !rangesOverlap(a.size, b.size)) return false;
  if (a.version && b.version && !rangesOverlap(a.version, b.version)) return false;

  if (a.path && b.path && !globsMayOverlap(a.path, b.path)) return false;
  if (a.path && b.pathNot && globExcludedByNot(a.path, b.pathNot)) return false;
  if (b.path && a.pathNot && globExcludedByNot(b.path, a.pathNot)) return false;

  const aAll = new Set(a.tagsAll ?? []);
  const bAll = new Set(b.tagsAll ?? []);
  if ((a.tagsNone ?? []).some((t) => bAll.has(t))) return false;
  if ((b.tagsNone ?? []).some((t) => aAll.has(t))) return false;

  return true;
}

/** True when every file matching `inner` also matches `outer`. */
export function conditionCovers(outer: Condition, inner: Condition): boolean {
  if (outer.size && (!inner.size || !rangeContains(outer.size, inner.size))) return false;
  if (outer.version && (!inner.version || !rangeContains(outer.version, inner.version))) return false;
  if (outer.path !== undefined && inner.path !== outer.path) return false;
  if (outer.pathNot !== undefined && inner.pathNot !== outer.pathNot) return false;

  const innerAll = new Set(inner.tagsAll ?? []);
  if ((outer.tagsAll ?? []).some((t) => !innerAll.has(t))) return false;
  const innerNone = new Set(inner.tagsNone ?? []);
  if ((outer.tagsNone ?? []).some((t) => !innerNone.has(t))) return false;

  return true;
}

function selfContradictions(rule: Rule): string[] {
  const problems: string[] = [];
  const c = rule.condition;
  if (c.size && c.size.min !== undefined && c.size.max !== undefined && c.size.min > c.size.max) {
    problems.push(`size range [${c.size.min}, ${c.size.max}] is empty`);
  }
  if (c.version && c.version.min !== undefined && c.version.max !== undefined && c.version.min > c.version.max) {
    problems.push(`version range [${c.version.min}, ${c.version.max}] is empty`);
  }
  const all = new Set(c.tagsAll ?? []);
  const both = (c.tagsNone ?? []).filter((t) => all.has(t));
  if (both.length > 0) {
    problems.push(`tags required and forbidden at once: ${both.join(', ')}`);
  }
  if (c.path !== undefined && c.pathNot !== undefined && globExcludedByNot(c.path, c.pathNot)) {
    problems.push(`path '${c.path}' is fully excluded by pathNot '${c.pathNot}'`);
  }
  return problems;
}

/**
 * Statically check a policy for conflicts:
 *  - duplicate rule ids (error)
 *  - self-contradictory rules that can never match (error)
 *  - same-priority overlapping rules with different actions (error: ambiguous)
 *  - higher-priority rules fully covering a lower-priority rule (warning: shadowed)
 */
export function checkPolicy(rules: Rule[]): PolicyIssue[] {
  const issues: PolicyIssue[] = [];

  const seen = new Map<string, number>();
  for (const r of rules) {
    seen.set(r.id, (seen.get(r.id) ?? 0) + 1);
  }
  for (const [id, count] of seen) {
    if (count > 1) {
      issues.push({
        severity: 'error', kind: 'duplicate-id', ruleIds: [id],
        message: `rule id '${id}' appears ${count} times`,
      });
    }
  }

  for (const r of rules) {
    for (const problem of selfContradictions(r)) {
      issues.push({
        severity: 'error', kind: 'unsatisfiable', ruleIds: [r.id],
        message: `rule '${r.id}' can never match: ${problem}`,
      });
    }
  }

  for (let i = 0; i < rules.length; i++) {
    for (let j = i + 1; j < rules.length; j++) {
      const a = rules[i]!;
      const b = rules[j]!;
      if (!conditionsMayOverlap(a.condition, b.condition)) continue;

      if (a.priority === b.priority && a.action !== b.action) {
        issues.push({
          severity: 'error', kind: 'ambiguous', ruleIds: [a.id, b.id],
          message: `rules '${a.id}' and '${b.id}' have equal priority ${a.priority}, ` +
            `overlapping conditions and different actions ('${a.action}' vs '${b.action}')`,
        });
        continue;
      }

      const higher = a.priority > b.priority ? a : b;
      const lower = higher === a ? b : a;
      if (higher.priority !== lower.priority && conditionCovers(higher.condition, lower.condition)) {
        issues.push({
          severity: 'warning', kind: 'shadowed', ruleIds: [higher.id, lower.id],
          message: `rule '${lower.id}' (priority ${lower.priority}) is fully covered by ` +
            `'${higher.id}' (priority ${higher.priority}) and can never take effect`,
        });
      }
    }
  }

  return issues;
}
