import { Condition, ConditionNode, Snapshot } from './types';
import { deepEqual, show } from './util';

function leaf(kind: string, result: boolean, summary: string): ConditionNode {
  return { kind, result, summary };
}

function make(description: string, evaluate: (s: Snapshot) => boolean, explain: (s: Snapshot) => ConditionNode): Condition {
  return { description, evaluate, explain };
}

function compare(field: string, expected: unknown, op: string, test: (actual: unknown) => boolean): Condition {
  const description = `${field} ${op} ${show(expected)}`;
  return make(
    description,
    (s) => test(s[field]),
    (s) => leaf('compare', test(s[field]), `${description} (actual: ${show(s[field])})`),
  );
}

export const eq = (field: string, expected: unknown): Condition =>
  compare(field, expected, '==', (a) => deepEqual(a, expected));
export const ne = (field: string, expected: unknown): Condition =>
  compare(field, expected, '!=', (a) => !deepEqual(a, expected));
export const gt = (field: string, expected: number): Condition =>
  compare(field, expected, '>', (a) => typeof a === 'number' && a > expected);
export const ge = (field: string, expected: number): Condition =>
  compare(field, expected, '>=', (a) => typeof a === 'number' && a >= expected);
export const lt = (field: string, expected: number): Condition =>
  compare(field, expected, '<', (a) => typeof a === 'number' && a < expected);
export const le = (field: string, expected: number): Condition =>
  compare(field, expected, '<=', (a) => typeof a === 'number' && a <= expected);
export const isIn = (field: string, expected: readonly unknown[]): Condition =>
  compare(field, expected, 'in', (a) => expected.some((e) => deepEqual(a, e)));
export const exists = (field: string): Condition =>
  make(
    `exists(${field})`,
    (s) => Object.prototype.hasOwnProperty.call(s, field),
    (s) => leaf('exists', Object.prototype.hasOwnProperty.call(s, field), `exists(${field})`),
  );

export function and(...conds: readonly Condition[]): Condition {
  return make(
    `(${conds.map((c) => c.description).join(' AND ')})`,
    (s) => conds.every((c) => c.evaluate(s)),
    (s) => ({
      kind: 'and',
      result: conds.every((c) => c.evaluate(s)),
      summary: 'AND',
      children: conds.map((c) => c.explain(s)),
    }),
  );
}

export function or(...conds: readonly Condition[]): Condition {
  return make(
    `(${conds.map((c) => c.description).join(' OR ')})`,
    (s) => conds.some((c) => c.evaluate(s)),
    (s) => ({
      kind: 'or',
      result: conds.some((c) => c.evaluate(s)),
      summary: 'OR',
      children: conds.map((c) => c.explain(s)),
    }),
  );
}

export function not(cond: Condition): Condition {
  return make(
    `NOT(${cond.description})`,
    (s) => !cond.evaluate(s),
    (s) => ({ kind: 'not', result: !cond.evaluate(s), summary: 'NOT', children: [cond.explain(s)] }),
  );
}

export const always: Condition = make('true', () => true, () => leaf('const', true, 'true'));
export const never: Condition = make('false', () => false, () => leaf('const', false, 'false'));

/** Escape hatch for arbitrary predicates. Evidence only records the boolean outcome. */
export function custom(description: string, predicate: (snapshot: Snapshot) => boolean): Condition {
  return make(
    description,
    predicate,
    (s) => leaf('custom', predicate(s), description),
  );
}
