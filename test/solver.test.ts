import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Solver } from '../src/solver.ts';

test('propagation forces the rest of an exactlyOne to absent', () => {
  const s = new Solver();
  const [a, b, c] = [s.addVar(), s.addVar(), s.addVar()];
  s.addConstraint({ id: 'x1', vars: [a, b, c], min: 1, max: 1 });
  s.assign(a, 1);
  assert.equal(s.propagate(), true);
  assert.equal(s.value(b), 0);
  assert.equal(s.value(c), 0);
});

test('propagation forces remaining vars present when min is tight', () => {
  const s = new Solver();
  const [a, b] = [s.addVar(), s.addVar()];
  s.addConstraint({ id: 'al1', vars: [a, b], min: 1, max: 2 });
  s.assign(a, 0);
  assert.equal(s.propagate(), true);
  assert.equal(s.value(b), 1);
});

test('push/pop restores assignments, constraints and variables', () => {
  const s = new Solver();
  const a = s.addVar();
  s.addConstraint({ id: 'keep', vars: [a], min: 0, max: 1 });

  s.push();
  const b = s.addVar();
  s.addConstraint({ id: 'temp', vars: [a, b], min: 2, max: 2 });
  assert.equal(s.propagate(), true);
  assert.equal(s.value(a), 1);
  assert.equal(s.value(b), 1);
  s.pop();

  assert.equal(s.varCount, 1, 'variable added inside the scope must be gone');
  assert.equal(s.value(a), -1, 'propagated assignment must be undone');
  assert.equal(s.check().sat, true, 'only the kept constraint remains');
});

test('pop without push throws', () => {
  assert.throws(() => new Solver().pop(), /without matching push/);
});

test('check detects joint unsat that propagation alone cannot see', () => {
  // x+y+z <= 1 with x+y>=1, y+z>=1, x+z>=1: bounds-consistent but unsat.
  const s = new Solver();
  const [x, y, z] = [s.addVar(), s.addVar(), s.addVar()];
  s.addConstraint({ id: 'cap', vars: [x, y, z], min: 0, max: 1 });
  s.addConstraint({ id: 'p1', vars: [x, y], min: 1, max: 2 });
  s.addConstraint({ id: 'p2', vars: [y, z], min: 1, max: 2 });
  s.addConstraint({ id: 'p3', vars: [x, z], min: 1, max: 2 });
  assert.equal(s.propagate(), true, 'propagation finds no direct conflict');
  assert.equal(s.check().sat, false, 'enumeration must catch the joint conflict');
});

test('check returns a witness that satisfies every constraint', () => {
  const s = new Solver();
  const [a, b, c] = [s.addVar(), s.addVar(), s.addVar()];
  s.addConstraint({ id: 'one', vars: [a, b], min: 1, max: 1 });
  s.addConstraint({ id: 'need', vars: [c], min: 1, max: 1 });
  const result = s.check();
  assert.equal(result.sat, true);
  const [wa, wb, wc] = result.witness as boolean[];
  assert.equal(Number(wa) + Number(wb), 1);
  assert.equal(wc, true);
  // state restored after the query
  assert.deepEqual([s.value(a), s.value(b), s.value(c)], [-1, -1, -1]);
});
