import { createHash } from 'node:crypto';

/**
 * Policy DSL. Deliberately restricted:
 *  - reads only explicitly named attributes,
 *  - integer comparisons and finite-set membership only,
 *  - boolean combinators (and/or/not) with short-circuit semantics.
 * No external identity service calls are possible from a rule.
 */
export type IntOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Rule =
  | { kind: 'intCmp'; attr: string; op: IntOp; value: number }
  | { kind: 'inSet'; attr: string; set: readonly number[] }
  | { kind: 'not'; arg: Rule }
  | { kind: 'and'; args: readonly Rule[] }
  | { kind: 'or'; args: readonly Rule[] };

export interface Policy {
  id: string;
  version: string;
  rule: Rule;
}

export interface RuleSummary {
  id: string;
  version: string;
  hash: string;
}

function assertInteger(value: number, where: string): void {
  if (!Number.isInteger(value)) {
    throw new Error(`rule validation: ${where} must be an integer, got ${value}`);
  }
}

export function intCmp(attr: string, op: IntOp, value: number): Rule {
  assertInteger(value, `intCmp(${attr}) value`);
  return { kind: 'intCmp', attr, op, value };
}

export function inSet(attr: string, set: readonly number[]): Rule {
  if (set.length === 0) throw new Error('rule validation: inSet requires a non-empty finite set');
  for (const v of set) assertInteger(v, `inSet(${attr}) member`);
  return { kind: 'inSet', attr, set: [...set] };
}

export function not(arg: Rule): Rule {
  return { kind: 'not', arg };
}

export function and(...args: readonly Rule[]): Rule {
  if (args.length === 0) throw new Error('rule validation: and() requires at least one argument');
  return { kind: 'and', args: [...args] };
}

export function or(...args: readonly Rule[]): Rule {
  if (args.length === 0) throw new Error('rule validation: or() requires at least one argument');
  return { kind: 'or', args: [...args] };
}

/** Canonical JSON-able form: sorted sets, deduped members, fixed key order. */
export function canonicalize(rule: Rule): unknown {
  switch (rule.kind) {
    case 'intCmp':
      return { kind: 'intCmp', attr: rule.attr, op: rule.op, value: rule.value };
    case 'inSet':
      return { kind: 'inSet', attr: rule.attr, set: [...new Set(rule.set)].sort((a, b) => a - b) };
    case 'not':
      return { kind: 'not', arg: canonicalize(rule.arg) };
    case 'and':
      return { kind: 'and', args: rule.args.map(canonicalize) };
    case 'or':
      return { kind: 'or', args: rule.args.map(canonicalize) };
  }
}

function sha256(text: string): string {
  return createHash('sha256').update(text).digest('hex');
}

/** Structural hash of a (sub-)rule. Used as the subproof key in the trace DAG. */
export function hashRule(rule: Rule): string {
  return sha256(JSON.stringify(canonicalize(rule)));
}

/** Rule summary binds policy id + version + rule body. */
export function summarizePolicy(policy: Policy): RuleSummary {
  const hash = sha256(
    JSON.stringify({ id: policy.id, version: policy.version, rule: canonicalize(policy.rule) }),
  );
  return { id: policy.id, version: policy.version, hash };
}
