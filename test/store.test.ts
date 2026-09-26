import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConstraintStore, explainUnsat, minimizeConflict } from '../src/store.ts';
import { solve, v, k, type Constraint, type VarDecl } from '../src/solver.ts';

const d = (lo: number, hi: number): number[] =>
  Array.from({ length: hi - lo + 1 }, (_, i) => lo + i);

const vars: VarDecl[] = [
  { name: 'x', domain: d(0, 5) },
  { name: 'y', domain: d(0, 5) },
];

test('incremental: add constraints to unsat, retract to sat', () => {
  const store = new ConstraintStore(vars);
  store.add('c1', { kind: 'eq', a: v('x'), b: k(2) });
  store.add('c2', { kind: 'eq', a: v('x'), b: k(4) });
  assert.equal(store.check().status, 'unsat');

  // Retraction fully withdraws the old constraint's influence.
  assert.equal(store.retract('c2'), true);
  const r = store.check();
  assert.equal(r.status, 'sat');
  assert.equal(r.model!.x, 2);
});

test('incremental correction is verified against independent enumeration', () => {
  const store = new ConstraintStore(vars);
  store.add('c1', { kind: 'sum', terms: [{ var: 'x', coeff: 1 }, { var: 'y', coeff: 1 }], op: '<=', rhs: 4 });
  store.add('c2', { kind: 'diff', a: 'x', b: 'y', op: '>=', c: 2 });
  const before = store.checkVerified();
  assert.equal(before.result.status, 'sat');
  assert.ok(before.verification.consistent);

  store.add('c3', { kind: 'eq', a: v('y'), b: k(5) });
  const after = store.checkVerified();
  assert.equal(after.result.status, 'unsat');
  assert.ok(after.verification.consistent, 'unsat confirmed by enumeration');

  store.retract('c3');
  const restored = store.checkVerified();
  assert.equal(restored.result.status, 'sat');
  assert.ok(restored.verification.consistent);
});

test('minimal conflict: subset-minimal and irreducible', () => {
  const entries: [string, Constraint][] = [
    ['c1', { kind: 'eq', a: v('x'), b: k(1) }],
    ['c2', { kind: 'eq', a: v('x'), b: k(3) }],   // conflicts with c1
    ['c3', { kind: 'eq', a: v('y'), b: k(2) }],   // irrelevant
  ];
  const { core, exact } = minimizeConflict(vars, entries);
  assert.ok(exact);
  assert.equal(core.length, 2);
  assert.deepEqual(new Set(core), new Set(['c1', 'c2']));
  // irreducible: removing any member restores satisfiability
  for (const id of core) {
    const rest = entries.filter(([eid]) => eid !== id && core.includes(eid));
    assert.equal(solve(vars, rest.map(([, c]) => c)).status, 'sat');
  }
});

test('explainUnsat returns a model-free result plus the conflict core', () => {
  const entries: [string, Constraint][] = [
    ['lo', { kind: 'diff', a: 'x', b: 'y', op: '>=', c: 4 }],
    ['hi', { kind: 'diff', a: 'x', b: 'y', op: '<=', c: 1 }],
    ['noise', { kind: 'eq', a: v('y'), b: k(0) }],
  ];
  const { result, conflict } = explainUnsat(vars, entries);
  assert.equal(result.status, 'unsat');
  assert.ok(conflict);
  assert.deepEqual(new Set(conflict!.core), new Set(['lo', 'hi']));
});

test('budget exhaustion during minimization is flagged as inexact', () => {
  const entries: [string, Constraint][] = [
    ['c1', { kind: 'eq', a: v('x'), b: k(1) }],
    ['c2', { kind: 'eq', a: v('x'), b: k(3) }],
  ];
  const { exact } = minimizeConflict(vars, entries, { budget: 0 });
  assert.equal(exact, false);
});
