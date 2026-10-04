import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { interval, show } from '../src/interval.ts';
import { IntervalUnion } from '../src/union.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));

test('segments are sorted and overlapping ones merged', () => {
  const u = IntervalUnion.fromSegments([iv('5', '6'), iv('1', '2'), iv('2', '3'), iv('9', '9')]);
  assert.equal(u.segments.length, 3);
  assert.equal(u.toString(), '[1, 3] ∪ [5, 6] ∪ [9, 9]');
  assert.ok(!u.precisionLoss);
});

test('division across zero yields a two-ray union', () => {
  const u = IntervalUnion.div(iv('1', '2'), iv('-1', '3'), 6, 'split');
  assert.equal(u.segments.length, 2);
  assert.ok(u.containsPoint(d('-1000')));
  assert.ok(u.containsPoint(d('1000')));
  assert.ok(!u.containsPoint(d('0')));
  assert.ok(!u.precisionLoss);
});

test('exceeding the segment limit collapses to hull with a precision-loss note', () => {
  const segs = [iv('0', '1'), iv('3', '4'), iv('6', '7'), iv('9', '10')];
  const u = IntervalUnion.fromSegments(segs, 2);
  assert.equal(u.segments.length, 1);
  assert.equal(show(u.segments[0]!), '[0, 10]');
  assert.ok(u.precisionLoss);
  assert.match(u.note ?? '', /precision loss/);
  assert.match(u.note ?? '', /exceeds limit 2/);
  // hull still conservatively contains every original point
  for (const s of segs) {
    if (s.kind === 'bounded') {
      assert.ok(u.containsPoint(s.lo!));
      assert.ok(u.containsPoint(s.hi!));
    }
  }
});

test('empty union is distinct from a union containing the unknown interval', () => {
  const empty = IntervalUnion.empty();
  assert.ok(empty.isEmpty());
  assert.ok(!empty.containsPoint(d('0')));
  const everything = IntervalUnion.fromSegments([interval(null, null)]);
  assert.ok(everything.containsPoint(d('123')));
  assert.notEqual(empty.toString(), everything.toString());
});
