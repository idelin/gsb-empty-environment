/**
 * End-to-end sample: rule evaluation + finite-domain solving + incremental
 * correction + minimal conflict. Run: npm run sample
 */
import { evaluate } from '../src/evaluator.ts';
import { field, literal, type Rule } from '../src/rules.ts';
import { solve, v, k, type Constraint, type VarDecl } from '../src/solver.ts';
import { ConstraintStore, explainUnsat } from '../src/store.ts';

const show = (title: string, data: unknown): void => {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(data, null, 2));
};

// ---------- 1. Rule evaluation on a JSON instance ----------
const rules: Rule[] = [
  { kind: 'compare', op: '<=', left: field('/start'), right: field('/end') },
  {
    kind: 'requiredIf',
    when: { kind: 'compare', op: '==', left: field('/kind'), right: literal('vip') },
    path: '/discount',
  },
  { kind: 'sum', listPath: '/items', itemPath: '/price', op: '<=', limit: 100 },
];

show('pass: well-formed instance', evaluate({
  start: 1, end: 5, kind: 'basic', items: [{ price: 30 }, { price: 40 }],
}, rules));

show('fail: end < start reports BOTH paths', evaluate({
  start: 9, end: 2, kind: 'basic', items: [],
}, rules));

show('unknown: condition field missing (not silently skipped)', evaluate({
  start: 1, end: 5, items: [{ price: 10 }],
}, rules));

show('unknown: missing list is not the empty list', evaluate({
  start: 1, end: 5, kind: 'basic',
}, rules));

show('type-error: type check precedes relation', evaluate({
  start: 1, end: 'soon', kind: 'basic', items: [],
}, rules));

// ---------- 2. Finite-domain solving: sum + diff + conditional branch ----------
const vars: VarDecl[] = [
  { name: 'x', domain: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] },
  { name: 'y', domain: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] },
  { name: 'flag', domain: [0, 1] },
];
const constraints: Constraint[] = [
  { kind: 'sum', terms: [{ var: 'x', coeff: 1 }, { var: 'y', coeff: 1 }], op: '==', rhs: 10 },
  { kind: 'diff', a: 'x', b: 'y', op: '>=', c: 2 },
  { kind: 'implies', cond: { var: 'flag', op: '==', value: 1 }, then: { kind: 'eq', a: v('x'), b: k(8) } },
];
show('sat: sum + diff + conditional branch', solve(vars, constraints));

show('unknown: budget exhausted (distinct from unsat)',
  solve(vars, constraints, { budget: 1 }));

// ---------- 3. Unsat => subset-minimal conflict ----------
const conflictEntries: [string, Constraint][] = [
  ['lower-bound', { kind: 'diff', a: 'x', b: 'y', op: '>=', c: 8 }],
  ['upper-bound', { kind: 'diff', a: 'x', b: 'y', op: '<=', c: 3 }],
  ['unrelated', { kind: 'eq', a: v('flag'), b: k(0) }],
];
show('unsat: subset-minimal conflict', explainUnsat(vars, conflictEntries));

// ---------- 4. Incremental correction with enumeration cross-check ----------
const store = new ConstraintStore(vars);
store.add('a', { kind: 'eq', a: v('x'), b: k(2) });
store.add('b', { kind: 'eq', a: v('x'), b: k(5) });
show('incremental: contradictory additions', store.checkVerified());

store.retract('b'); // withdraw the old constraint's influence
store.add('c', { kind: 'diff', a: 'y', b: 'x', op: '>=', c: 1 });
show('incremental: after retract + correct (verified)', store.checkVerified());
