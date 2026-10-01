import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  aggregate,
  CaliberMismatchError,
  mergeAggregates,
  metricRef,
  missingField,
  obs,
  present,
  ratioOf,
  retractedField,
  type Observation,
} from '../src/measure.ts';
import { equal, rational } from '../src/rational.ts';

const metric = metricRef('conversion-rate', 'v2');

test('1/2 merged with 9/10 gives 10/12, not the average of percentages', () => {
  const agg = aggregate([obs('r1', metric, 1n, 2n), obs('r2', metric, 9n, 10n)]);
  assert.equal(agg.numeratorSum, 10n);
  assert.equal(agg.denominatorSum, 12n);
  const ratio = ratioOf(agg);
  assert.equal(ratio.kind, 'value');
  if (ratio.kind === 'value') {
    assert.ok(equal(ratio.value, rational(5n, 6n))); // 83.33...%
    // naive average of 50% and 90% would be 7/10 -- explicitly not this
    assert.ok(!equal(ratio.value, rational(7n, 10n)));
  }
});

test('missing fields poison the ratio (missing propagation)', () => {
  const records: Observation[] = [
    obs('a', metric, 3n, 10n),
    { id: 'b', metric, numerator: missingField(), denominator: present(5n) },
  ];
  const agg = aggregate(records);
  assert.equal(agg.numeratorMissing, 1);
  const ratio = ratioOf(agg);
  assert.deepEqual(ratio, {
    kind: 'missing',
    missingNumerator: true,
    missingDenominator: false,
  });
});

test('retracted denominator is excluded, audited, and does not poison', () => {
  const records: Observation[] = [
    obs('a', metric, 3n, 10n),
    { id: 'b', metric, numerator: present(4n), denominator: retractedField() },
  ];
  const agg = aggregate(records);
  assert.equal(agg.denominatorRetracted, 1);
  assert.equal(agg.unpaired, 1);
  assert.equal(agg.pairCount, 1);
  assert.equal(agg.numeratorSum, 3n);
  assert.equal(agg.denominatorSum, 10n);
  const ratio = ratioOf(agg);
  assert.equal(ratio.kind, 'value');
  if (ratio.kind === 'value') assert.ok(equal(ratio.value, rational(3n, 10n)));
});

test('zero denominator is distinct from a true zero ratio', () => {
  const zeroDen = ratioOf(aggregate([obs('z', metric, 5n, 0n)]));
  assert.deepEqual(zeroDen, { kind: 'zero-denominator', numerator: 5n });

  const trueZero = ratioOf(aggregate([obs('t', metric, 0n, 7n)]));
  assert.equal(trueZero.kind, 'value');
  if (trueZero.kind === 'value') {
    assert.ok(equal(trueZero.value, rational(0n, 1n)));
  }
  assert.notEqual(zeroDen.kind, trueZero.kind);
});

test('mixing calibers (metric versions) is rejected', () => {
  const v1 = metricRef('conversion-rate', 'v1');
  assert.throws(
    () => aggregate([obs('a', metric, 1n, 2n), obs('b', v1, 1n, 2n)]),
    CaliberMismatchError,
  );
  assert.throws(
    () =>
      mergeAggregates([
        aggregate([obs('a', metric, 1n, 2n)]),
        aggregate([obs('b', v1, 1n, 2n)]),
      ]),
    CaliberMismatchError,
  );
});

test('multi-level aggregation equals flat aggregation', () => {
  const jan = [obs('j1', metric, 1n, 2n), obs('j2', metric, 9n, 10n)];
  const feb = [obs('f1', metric, 4n, 5n), obs('f2', metric, 2n, 3n)];
  const flat = aggregate([...jan, ...feb]);
  const hierarchical = mergeAggregates([aggregate(jan), aggregate(feb)]);
  assert.equal(hierarchical.numeratorSum, flat.numeratorSum);
  assert.equal(hierarchical.denominatorSum, flat.denominatorSum);
  assert.equal(hierarchical.pairCount, flat.pairCount);
});

test('aggregating an empty input is rejected', () => {
  assert.throws(() => aggregate([]), RangeError);
  assert.throws(() => mergeAggregates([]), RangeError);
});
