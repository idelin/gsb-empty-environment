import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measurement, boxOf } from '../src/measurement.ts';
import { constraint } from '../src/constraint.ts';
import { classifyConstraint } from '../src/classify.ts';
import { gridPoints, enumerateVerify } from '../src/enumerate.ts';

test('small-domain enumeration confirms classifier boundary behavior', () => {
  const ms = [measurement('x', '1', '1'), measurement('y', '0.5', '0.5')];
  const box = boxOf(ms);
  const constraints = [
    constraint('c-pass', { x: '1', y: '1' }, 'le', '3'),  // max sum = 3, closed -> pass
    constraint('c-open', { x: '1', y: '1' }, 'lt', '3'),  // max sum = 3, open -> review
    constraint('c-mid', { x: '1', y: '1' }, 'le', '2'),   // straddles -> review
    constraint('c-fail', { x: '1', y: '1' }, 'lt', '0'),  // min sum = 0, open -> fail
  ];
  const points = gridPoints(ms, 4);
  assert.equal(points.length, 25);
  const result = enumerateVerify(constraints, points);

  for (const c of constraints) {
    const v = classifyConstraint(c, box);
    const counts = result.perConstraint.get(c.id)!;
    if (v.status === 'pass') assert.equal(counts.fail, 0, `${c.id} must have no failing grid point`);
    if (v.status === 'fail') assert.equal(counts.pass, 0, `${c.id} must have no passing grid point`);
    if (v.status === 'review') {
      assert.ok(counts.pass > 0, `${c.id} must have passing grid points`);
      assert.ok(counts.fail > 0, `${c.id} must have failing grid points`);
    }
  }
  // c-open: only the corner (2,1) attains sum 3 -> exactly one failing grid point
  assert.equal(result.perConstraint.get('c-open')!.fail, 1);
});

test('grid endpoints are exact rationals, including the box corners', () => {
  const ms = [measurement('x', '0.5', '0.5')];
  const points = gridPoints(ms, 2);
  assert.deepEqual(
    points.map((p) => p.get('x')!.toString()),
    ['0', '1/2', '1'],
  );
});

test('enumeration refuses unbounded (unknown-error) domains', () => {
  assert.throws(() => gridPoints([measurement('x', '1', 'unknown')], 2), /unbounded/);
});

test('grid size is capped', () => {
  const ms = [measurement('x', '0', '1'), measurement('y', '0', '1')];
  assert.throws(() => gridPoints(ms, 2000, 1000), /too large/);
});
