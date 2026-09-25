import { canonicalize } from './canonical.ts';

export type IntOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';
export type Scalar = string | number;

/**
 * Policy rules are deliberately small and self-contained:
 * finite-set membership, integer comparisons and boolean combinators.
 * They read explicit attributes only; no external identity services.
 */
export type ConditionRule =
  | { kind: 'in'; attr: string; set: Scalar[] }
  | { kind: 'int'; attr: string; op: IntOp; value: number };

export type Rule =
  | ConditionRule
  | { kind: 'not'; child: Rule }
  | { kind: 'and'; children: Rule[] }
  | { kind: 'or'; children: Rule[] };

export interface Policy {
  name: string;
  version: number;
  root: Rule;
}

/** Canonical rule form: `in` sets are de-duplicated and sorted. */
export function normalizeRule(rule: Rule): Rule {
  switch (rule.kind) {
    case 'in': {
      const set = [...new Set(rule.set)].sort((a, b) =>
        String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0,
      );
      return { kind: 'in', attr: rule.attr, set };
    }
    case 'int':
      return { kind: 'int', attr: rule.attr, op: rule.op, value: rule.value };
    case 'not':
      return { kind: 'not', child: normalizeRule(rule.child) };
    case 'and':
      return { kind: 'and', children: rule.children.map(normalizeRule) };
    case 'or':
      return { kind: 'or', children: rule.children.map(normalizeRule) };
  }
}

export function normalizePolicy(policy: Policy): Policy {
  return { name: policy.name, version: policy.version, root: normalizeRule(policy.root) };
}

/** Stable key used for shared-subproof compression and rule comparison. */
export function ruleKey(rule: Rule): string {
  return canonicalize(normalizeRule(rule));
}

export function evalCondition(cond: ConditionRule, value: unknown): boolean {
  if (cond.kind === 'in') {
    return cond.set.some((member) => member === value);
  }
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new Error(`attribute "${cond.attr}" must be an integer, got ${JSON.stringify(value)}`);
  }
  if (!Number.isInteger(cond.value)) {
    throw new Error(`condition on "${cond.attr}" must compare against an integer`);
  }
  switch (cond.op) {
    case 'eq': return value === cond.value;
    case 'ne': return value !== cond.value;
    case 'lt': return value < cond.value;
    case 'le': return value <= cond.value;
    case 'gt': return value > cond.value;
    case 'ge': return value >= cond.value;
  }
}
