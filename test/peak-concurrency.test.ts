import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PeakConcurrencyTracker } from '../src/peak-concurrency.ts';

test('empty tracker reports zero peak and no segments', () => {
  const tracker = new PeakConcurrencyTracker();
  assert.deepEqual(tracker.peak(), { value: 0, segments: [] });
  assert.equal(tracker.size, 0);
});

test('acceptance: adjacent half-open intervals [0,5) and [5,9) have concurrency 1', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  const peak = tracker.add('b', 5, 9);
  assert.equal(peak.value, 1);
  assert.deepEqual(peak.segments, [{ start: 0, end: 9 }]);
});

test('acceptance: late backfill of [4,6) raises peak to 2 over [4,6)', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  tracker.add('b', 5, 9);
  const peak = tracker.add('late', 4, 6);
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [{ start: 4, end: 6 }]);
});

test('acceptance: retracting the backfilled interval restores peak to 1', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  tracker.add('b', 5, 9);
  tracker.add('late', 4, 6);
  const peak = tracker.remove('late');
  assert.equal(peak.value, 1);
  assert.deepEqual(peak.segments, [{ start: 0, end: 9 }]);
});

test('endpoint closure: touching endpoints never double-count at the boundary', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  tracker.add('b', 5, 9);
  tracker.add('c', 9, 12);
  const peak = tracker.peak();
  assert.equal(peak.value, 1);
  assert.deepEqual(peak.segments, [{ start: 0, end: 12 }]);
});

test('out-of-order arrival yields the same result as in-order arrival', () => {
  const inOrder = new PeakConcurrencyTracker();
  inOrder.add('a', 0, 5);
  inOrder.add('b', 5, 9);
  inOrder.add('late', 4, 6);

  const outOfOrder = new PeakConcurrencyTracker();
  outOfOrder.add('b', 5, 9);
  outOfOrder.add('late', 4, 6);
  outOfOrder.add('a', 0, 5);

  assert.deepEqual(outOfOrder.peak(), inOrder.peak());
});

test('fully contained overlap raises peak only inside the inner interval', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('outer', 0, 10);
  const peak = tracker.add('inner', 3, 6);
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [{ start: 3, end: 6 }]);
});

test('identical intervals stack to full concurrency', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  const peak = tracker.add('b', 0, 5);
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [{ start: 0, end: 5 }]);
});

test('disjoint peak regions are all reported as separate segments', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 4);
  tracker.add('b', 1, 2);
  tracker.add('c', 10, 14);
  const peak = tracker.add('d', 12, 13);
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [
    { start: 1, end: 2 },
    { start: 12, end: 13 },
  ]);
});

test('removing one of several intervals recomputes peak and segments', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 10);
  tracker.add('b', 2, 8);
  tracker.add('c', 4, 6);
  assert.equal(tracker.peak().value, 3);

  const peak = tracker.remove('c');
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [{ start: 2, end: 8 }]);
});

test('removing the last interval returns to zero peak', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0, 5);
  const peak = tracker.remove('a');
  assert.deepEqual(peak, { value: 0, segments: [] });
  assert.equal(tracker.size, 0);
});

test('rejects invalid intervals and unknown operations', () => {
  const tracker = new PeakConcurrencyTracker();
  assert.throws(() => tracker.add('bad', 5, 5), RangeError);
  assert.throws(() => tracker.add('bad', 6, 5), RangeError);
  assert.throws(() => tracker.add('bad', Number.NaN, 5), RangeError);
  assert.throws(() => tracker.remove('missing'), /Unknown interval id/);

  tracker.add('a', 0, 5);
  assert.throws(() => tracker.add('a', 1, 2), /Duplicate interval id/);
});

test('fractional timestamps are supported', () => {
  const tracker = new PeakConcurrencyTracker();
  tracker.add('a', 0.5, 2.5);
  const peak = tracker.add('b', 1.5, 3.5);
  assert.equal(peak.value, 2);
  assert.deepEqual(peak.segments, [{ start: 1.5, end: 2.5 }]);
});
