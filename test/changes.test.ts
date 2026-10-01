import { test } from 'node:test';
import assert from 'node:assert/strict';

import { addChanges, ppChange, relChange, UnitMismatchError } from '../src/changes.ts';
import { aggregate, metricRef, obs, ratioOf, type RatioValue } from '../src/measure.ts';
import { equal, rational } from '../src/rational.ts';

const metric = metricRef('conversion-rate', 'v2');

function ratioOfPair(numerator: bigint, denominator: bigint): RatioValue {
  return ratioOf(aggregate([obs('x', metric, numerator, denominator)]));
}

test('percentage-point change: 50% -> 60% is +10 pp', () => {
  const change = ppChange(ratioOfPair(3n, 5n), ratioOfPair(1n, 2n));
  assert.equal(change.kind, 'defined');
  assert.equal(change.unit, 'pp');
  if (change.kind === 'defined') assert.ok(equal(change.value, rational(10n, 1n)));
});

test('relative change: 50% -> 60% is +20 percent, not +10', () => {
  const change = relChange(ratioOfPair(3n, 5n), ratioOfPair(1n, 2n));
  assert.equal(change.kind, 'defined');
  assert.equal(change.unit, 'percent');
  if (change.kind === 'defined') assert.ok(equal(change.value, rational(20n, 1n)));
});

test('relative growth from an old value of 0 is undefined', () => {
  const change = relChange(ratioOfPair(1n, 10n), ratioOfPair(0n, 10n));
  assert.deepEqual(change, { kind: 'undefined', unit: 'percent', reason: 'base-zero' });
});

test('percentage-point change from a zero base stays defined', () => {
  const change = ppChange(ratioOfPair(1n, 20n), ratioOfPair(0n, 10n));
  assert.equal(change.kind, 'defined');
  if (change.kind === 'defined') assert.ok(equal(change.value, rational(5n, 1n))); // +5 pp
});

test('missing or zero-denominator inputs make changes undefined', () => {
  const missing = ratioOf(
    aggregate([
      {
        id: 'm',
        metric,
        numerator: { status: 'missing' },
        denominator: { status: 'present', value: 5n },
      },
    ]),
  );
  assert.equal(ppChange(missing, ratioOfPair(1n, 2n)).kind, 'undefined');
  assert.equal(relChange(ratioOfPair(1n, 2n), missing).kind, 'undefined');

  const zeroDen = ratioOf(aggregate([obs('z', metric, 3n, 0n)]));
  assert.equal(ppChange(zeroDen, ratioOfPair(1n, 2n)).kind, 'undefined');
  assert.equal(relChange(ratioOfPair(1n, 2n), zeroDen).kind, 'undefined');
});

test('percentage points and percentages cannot be combined', () => {
  const pp = ppChange(ratioOfPair(3n, 5n), ratioOfPair(1n, 2n));
  const rel = relChange(ratioOfPair(3n, 5n), ratioOfPair(1n, 2n));
  assert.throws(() => addChanges(pp, rel), UnitMismatchError);
});

test('same-unit changes combine exactly', () => {
  const a = ppChange(ratioOfPair(3n, 5n), ratioOfPair(1n, 2n)); // +10 pp
  const b = ppChange(ratioOfPair(7n, 10n), ratioOfPair(3n, 5n)); // +10 pp
  const total = addChanges(a, b);
  assert.equal(total.kind, 'defined');
  if (total.kind === 'defined') assert.ok(equal(total.value, rational(20n, 1n)));
});
