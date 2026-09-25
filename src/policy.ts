/**
 * Policy = schema + rules + explicit grade thresholds.
 *
 * Rule kinds:
 *   - scoring rule: contributes its rational weight (negative allowed) when true
 *   - hard-reject rule: when true the subject is rejected outright; a hard
 *     rejection is never offset by any score, high or low
 *
 * Duplicate rule ids: identical content is deduplicated (counted once);
 * conflicting content under the same id rejects the whole policy.
 */

import type { Schema } from './schema.ts';
import { validateSchema } from './schema.ts';
import type { Condition } from './condition.ts';
import { canonicalCondition, conditionDeps, validateCondition } from './condition.ts';
import type { Rational, WeightSpec } from './rational.ts';
import { cmp, parseRational } from './rational.ts';

export interface ScoringRuleSpec {
  id: string;
  when: Condition;
  weight: WeightSpec;
  hardReject?: false;
}

export interface HardRejectRuleSpec {
  id: string;
  when: Condition;
  hardReject: true;
}

export type RuleSpec = ScoringRuleSpec | HardRejectRuleSpec;

export interface ThresholdSpec {
  grade: string;
  /** inclusive lower bound of the segment, as a rational */
  min: WeightSpec;
}

export interface PolicySpec {
  schema: Schema;
  rules: readonly RuleSpec[];
  /** explicit segments, listed from highest grade to lowest */
  thresholds: readonly ThresholdSpec[];
}

export interface ScoringRule {
  readonly kind: 'score';
  readonly id: string;
  readonly when: Condition;
  readonly weight: Rational;
  readonly deps: ReadonlySet<string>;
}

export interface HardRejectRule {
  readonly kind: 'reject';
  readonly id: string;
  readonly when: Condition;
  readonly deps: ReadonlySet<string>;
}

export type Rule = ScoringRule | HardRejectRule;

export interface Threshold {
  readonly grade: string;
  readonly min: Rational;
}

export interface Policy {
  readonly schema: Schema;
  readonly rules: readonly Rule[];
  readonly thresholds: readonly Threshold[];
  /** every attribute any rule reads */
  readonly usedAttributes: ReadonlySet<string>;
}

function canonicalRuleContent(spec: RuleSpec): string {
  const base: Record<string, unknown> = { when: canonicalCondition(spec.when) };
  if ('hardReject' in spec && spec.hardReject === true) {
    base.hardReject = true;
  } else {
    base.weight = parseRational((spec as ScoringRuleSpec).weight).num.toString() +
      '/' + parseRational((spec as ScoringRuleSpec).weight).den.toString();
  }
  return JSON.stringify(base);
}

/** score -> grade via explicit segments. Thresholds are sorted desc by min. */
export function gradeForScore(thresholds: readonly Threshold[], score: Rational): string {
  for (const t of thresholds) {
    if (cmp(score, t.min) >= 0) return t.grade;
  }
  // validation guarantees the lowest segment is unbounded below, so unreachable
  throw new Error('no matching threshold segment (policy misconfigured)');
}

export function loadPolicy(spec: PolicySpec): Policy {
  validateSchema(spec.schema);

  if (!Array.isArray(spec.thresholds) || spec.thresholds.length === 0) {
    throw new Error('policy needs at least one grade threshold');
  }
  const grades = new Set<string>();
  const thresholds: Threshold[] = spec.thresholds.map((t) => {
    if (grades.has(t.grade)) throw new Error(`duplicate grade label ${t.grade}`);
    grades.add(t.grade);
    return { grade: t.grade, min: parseRational(t.min) };
  });
  for (let i = 1; i < thresholds.length; i++) {
    if (cmp(thresholds[i - 1].min, thresholds[i].min) <= 0) {
      throw new Error('thresholds must be listed from highest to lowest with strictly decreasing mins');
    }
  }

  // Duplicate-id handling: group by id, dedupe identical, reject conflicts.
  const byId = new Map<string, { spec: RuleSpec; canonical: string }>();
  for (const rule of spec.rules) {
    if (typeof rule.id !== 'string' || rule.id.length === 0) {
      throw new Error('every rule needs a non-empty string id');
    }
    const isReject = 'hardReject' in rule && rule.hardReject === true;
    if (!isReject && (rule as ScoringRuleSpec).weight === undefined) {
      throw new Error(`rule ${rule.id} needs a weight or hardReject: true`);
    }
    validateCondition(spec.schema, rule.when);
    const canonical = canonicalRuleContent(rule);
    const prev = byId.get(rule.id);
    if (prev) {
      if (prev.canonical !== canonical) {
        throw new Error(
          `conflicting contents for duplicate rule id "${rule.id}" — policy rejected`,
        );
      }
      continue; // identical duplicate: count once
    }
    byId.set(rule.id, { spec: rule, canonical });
  }

  const rules: Rule[] = [];
  const used = new Set<string>();
  for (const { spec: rule } of byId.values()) {
    const deps = conditionDeps(rule.when);
    for (const d of deps) used.add(d);
    if ('hardReject' in rule && rule.hardReject === true) {
      rules.push({ kind: 'reject', id: rule.id, when: rule.when, deps });
    } else {
      rules.push({
        kind: 'score',
        id: rule.id,
        when: rule.when,
        weight: parseRational((rule as ScoringRuleSpec).weight),
        deps,
      });
    }
  }

  return { schema: spec.schema, rules, thresholds, usedAttributes: used };
}
