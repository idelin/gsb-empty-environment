import { canonicalCondition, validateCondition, PolicyValidationError, type Condition } from './condition.ts';
import { Rational, toRational, type RationalLike } from './rational.ts';

export interface Rule {
  id: string;
  when: Condition;
  /** Score contribution when `when` holds. May be negative. Defaults to 0. */
  weight?: RationalLike;
  /** When true and `when` holds, the subject is rejected outright. */
  hardReject?: boolean;
}

/**
 * Explicit score bands, ascending by `gte`. grade(score) is the band with the
 * largest `gte` <= score. The first band may use `gte: null` for "unbounded
 * below"; every other band must give an explicit threshold.
 */
export interface Band {
  grade: string;
  gte: RationalLike | null;
}

export interface PolicySpec {
  /** Finite domain of every attribute the policy may read. */
  attributes: Record<string, readonly number[]>;
  rules: readonly Rule[];
  bands: readonly Band[];
}

export interface NormalizedRule {
  id: string;
  when: Condition;
  weight: Rational;
  hardReject: boolean;
  attrs: ReadonlySet<string>;
}

export interface NormalizedBand {
  grade: string;
  /** null only for the first band (unbounded below). */
  gte: Rational | null;
}

export interface NormalizedPolicy {
  attributes: ReadonlyMap<string, readonly number[]>;
  rules: readonly NormalizedRule[];
  bands: readonly NormalizedBand[];
}

export class PolicyConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyConflictError';
  }
}

export function normalizePolicy(spec: PolicySpec): NormalizedPolicy {
  if (!spec || typeof spec !== 'object') throw new PolicyValidationError('policy spec required');

  const attributes = new Map<string, readonly number[]>();
  for (const [name, domain] of Object.entries(spec.attributes ?? {})) {
    if (!Array.isArray(domain) || domain.length === 0) {
      throw new PolicyValidationError(`attribute "${name}": domain must be a non-empty finite set`);
    }
    const seen = new Set<number>();
    for (const v of domain) {
      if (!Number.isInteger(v)) {
        throw new PolicyValidationError(`attribute "${name}": domain values must be integers, got ${v}`);
      }
      if (seen.has(v)) {
        throw new PolicyValidationError(`attribute "${name}": duplicate domain value ${v}`);
      }
      seen.add(v);
    }
    attributes.set(name, domain);
  }
  const declared = new Set(attributes.keys());

  const bands = normalizeBands(spec.bands);
  const rules = normalizeRules(spec.rules, declared);

  return { attributes, rules, bands };
}

function normalizeBands(bands: readonly Band[]): NormalizedBand[] {
  if (!Array.isArray(bands) || bands.length === 0) {
    throw new PolicyValidationError('policy requires at least one grade band');
  }
  const out: NormalizedBand[] = [];
  const grades = new Set<string>();
  let prev: Rational | null = null;
  bands.forEach((band, i) => {
    if (!band.grade || typeof band.grade !== 'string') {
      throw new PolicyValidationError(`bands[${i}]: grade must be a non-empty string`);
    }
    if (grades.has(band.grade)) {
      throw new PolicyValidationError(`bands[${i}]: duplicate grade "${band.grade}"`);
    }
    grades.add(band.grade);
    if (band.gte === null) {
      if (i !== 0) throw new PolicyValidationError(`bands[${i}]: only the first band may be unbounded below`);
      out.push({ grade: band.grade, gte: null });
      return;
    }
    const gte = toRational(band.gte);
    if (prev !== null && gte.le(prev)) {
      throw new PolicyValidationError(`bands[${i}]: thresholds must be strictly increasing`);
    }
    prev = gte;
    out.push({ grade: band.grade, gte });
  });
  return out;
}

function normalizeRules(rules: readonly Rule[], declared: ReadonlySet<string>): NormalizedRule[] {
  if (!Array.isArray(rules)) throw new PolicyValidationError('policy rules must be an array');
  const byId = new Map<string, { canonical: string }>();
  const out: NormalizedRule[] = [];
  rules.forEach((rule, i) => {
    const path = `rules[${i}]`;
    if (!rule.id || typeof rule.id !== 'string') {
      throw new PolicyValidationError(`${path}: rule id must be a non-empty string`);
    }
    validateCondition(rule.when, declared, `${path}.when`);
    const weight = rule.weight === undefined ? Rational.zero() : toRational(rule.weight);
    const hardReject = rule.hardReject === true;
    const canonical = `${canonicalCondition(rule.when)}|w=${weight.toString()}|hr=${hardReject}`;

    const existing = byId.get(rule.id);
    if (existing !== undefined) {
      if (existing.canonical !== canonical) {
        throw new PolicyConflictError(
          `rule id "${rule.id}" appears twice with conflicting content`,
        );
      }
      // Identical duplicate: counted once.
      return;
    }
    byId.set(rule.id, { canonical });
    out.push({
      id: rule.id,
      when: rule.when,
      weight,
      hardReject,
      attrs: attrsOf(rule.when),
    });
  });
  return out;
}

function attrsOf(cond: Condition): ReadonlySet<string> {
  const s = new Set<string>();
  collect(cond, s);
  return s;
}
function collect(cond: Condition, into: Set<string>): void {
  switch (cond.kind) {
    case 'const': return;
    case 'cmp':
    case 'in': into.add(cond.attr); return;
    case 'and':
    case 'or': cond.args.forEach((c) => collect(c, into)); return;
    case 'not': collect(cond.arg, into); return;
  }
}

/** Maps a definite score to its grade band. */
export function gradeForScore(bands: readonly NormalizedBand[], score: Rational): string {
  let grade = bands[0].grade;
  for (const band of bands) {
    if (band.gte === null || score.ge(band.gte)) grade = band.grade;
    else break;
  }
  return grade;
}
