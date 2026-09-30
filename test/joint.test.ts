import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measurement, boxOf } from '../src/measurement.ts';
import { constraint, satisfies } from '../src/constraint.ts';
import { judge, verifyWitness } from '../src/judge.ts';
import { verifyFarkas } from '../src/joint.ts';

const xyBox = () => boxOf([measurement('x', '0.5', '0.5'), measurement('y', '0.5', '0.5')]);

test('shared error variables: per-item "possible" must NOT merge into overall pass', () => {
  // x,y in [0,1]. Each constraint is individually satisfiable ("review"),
  // but the JOINT region is empty: x+y<=1 and x+y>=2 cannot hold together.
  const constraints = [
    constraint('sum-lo', { x: '1', y: '1' }, 'le', '1'),
    constraint('sum-hi', { x: '1', y: '1' }, 'ge', '2'),
  ];
  const j = judge(constraints, ['x', 'y'], xyBox());
  assert.equal(j.kind, 'fail');
  if (j.kind === 'fail') {
    assert.equal(j.certificate.type, 'farkas');
    if (j.certificate.type === 'farkas') {
      // Certificate independently verifiable: non-negative combination of
      // source rows deriving 0 <= negative constant.
      assert.ok(verifyFarkas(j.certificate.farkas, 2));
      assert.ok(j.certificate.farkas.constant.sign() < 0);
    }
  }
});

test('jointly feasible mixed region: witness + counterexample certificates', () => {
  // x in [0,2]; x <= 1 holds for part of the box only.
  const box = boxOf([measurement('x', '1', '1')]);
  const constraints = [constraint('cap', { x: '1' }, 'le', '1')];
  const j = judge(constraints, ['x'], box);
  assert.equal(j.kind, 'mixed');
  if (j.kind === 'mixed') {
    assert.ok(verifyWitness(j.witness, constraints, box));
    assert.equal(j.counterexample.type, 'corner');
    if (j.counterexample.type === 'corner') {
      assert.equal(j.counterexample.corner.get('x')?.toString(), '2');
      assert.ok(!satisfies(constraints[0], j.counterexample.corner));
    }
  }
});

test('definite pass over the whole box', () => {
  const constraints = [constraint('sum', { x: '1', y: '1' }, 'le', '2')];
  const j = judge(constraints, ['x', 'y'], xyBox());
  assert.equal(j.kind, 'pass');
});

test('single-constraint-everywhere-fail short-circuits with its certificate', () => {
  const constraints = [constraint('cap', { x: '1' }, 'lt', '0')]; // x in [0,1], x<0 impossible
  const j = judge(constraints, ['x', 'y'], xyBox());
  assert.equal(j.kind, 'fail');
  if (j.kind === 'fail') assert.equal(j.certificate.type, 'single-constraint');
});

test('insufficient budget is reported as pending, never as a verdict', () => {
  const constraints = [
    constraint('sum-lo', { x: '1', y: '1' }, 'le', '1'),
    constraint('sum-hi', { x: '1', y: '1' }, 'ge', '2'),
  ];
  const j = judge(constraints, ['x', 'y'], xyBox(), { maxCombinations: 0 });
  assert.equal(j.kind, 'pending');
  if (j.kind === 'pending') assert.match(j.reason, /budget/);
});

test('witness satisfies strict (open) constraints exactly', () => {
  // x in [0,1], y in [0,1]; x+y < 2 and x+y > 0 jointly feasible, not certain.
  const constraints = [
    constraint('open-hi', { x: '1', y: '1' }, 'lt', '2'),
    constraint('open-lo', { x: '1', y: '1' }, 'gt', '0'),
  ];
  const j = judge(constraints, ['x', 'y'], xyBox());
  assert.equal(j.kind, 'mixed');
  if (j.kind === 'mixed') {
    assert.ok(verifyWitness(j.witness, constraints, xyBox()));
    assert.ok(satisfies(constraints[0], j.witness));
    assert.ok(satisfies(constraints[1], j.witness));
  }
});

test('unknown error makes the region unbounded: mixed with unbounded counterexample', () => {
  const box = boxOf([measurement('x', '5', 'unknown')]);
  const constraints = [constraint('cap', { x: '1' }, 'le', '10')];
  const j = judge(constraints, ['x'], box);
  assert.equal(j.kind, 'mixed');
  if (j.kind === 'mixed') assert.equal(j.counterexample.type, 'unbounded');
});

test('equality-touching feasible point is found exactly', () => {
  // x+y = 1 exactly (via <= and >=) over [0,1]^2: feasible, not certain.
  const constraints = [
    constraint('eq-le', { x: '1', y: '1' }, 'le', '1'),
    constraint('eq-ge', { x: '1', y: '1' }, 'ge', '1'),
  ];
  const j = judge(constraints, ['x', 'y'], xyBox());
  assert.equal(j.kind, 'mixed');
  if (j.kind === 'mixed') {
    const sum = j.witness.get('x')!.add(j.witness.get('y')!);
    assert.equal(sum.toString(), '1');
  }
});
