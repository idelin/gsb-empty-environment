import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CalibrationTable,
  CalibrationConflictError,
  OutOfRangeError,
} from '../src/calibration.ts';
import { Rational } from '../src/rational.ts';

const OPTS = { scale: 6, rounding: 'half-up' as const, outOfRange: 'error' as const };

test('forward interpolates linearly and exactly', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '10', y: '3' },
  ]);
  const r = t.forward('5', OPTS)!;
  assert.equal(r.exact, '1.5');
  assert.equal(r.value, '1.500000');
  assert.equal(r.exactError, '0');
  assert.equal(r.clamped, false);
});

test('forward keeps non-terminating results exact and bounded after quantization', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '3', y: '1' },
  ]);
  const r = t.forward('1', { scale: 2, rounding: 'half-up', outOfRange: 'error' })!;
  assert.equal(r.exact, '1/3');
  assert.equal(r.value, '0.33');
  assert.equal(r.exactError, '1/300');
  assert.equal(r.errorBound, '0.005');
});

test('forward supports non-monotonic outputs and plateaus', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '10', y: '10' },
    { x: '15', y: '10' }, // plateau
    { x: '25', y: '0' }, // falling
  ]);
  assert.equal(t.forward('5', OPTS)!.value, '5.000000');
  assert.equal(t.forward('12', OPTS)!.value, '10.000000'); // on the plateau
  assert.equal(t.forward('20', OPTS)!.value, '5.000000'); // falling slope
});

test('forward out-of-range policies are explicit', () => {
  const t = new CalibrationTable([
    { x: '0', y: '100' },
    { x: '10', y: '200' },
  ]);
  assert.throws(() => t.forward('-1', OPTS), OutOfRangeError);
  assert.throws(() => t.forward('11', OPTS), OutOfRangeError);
  assert.equal(t.forward('-1', { ...OPTS, outOfRange: 'null' }), null);
  const lo = t.forward('-5', { ...OPTS, outOfRange: 'clamp' })!;
  assert.equal(lo.value, '100.000000');
  assert.equal(lo.clamped, true);
  const hi = t.forward('99', { ...OPTS, outOfRange: 'clamp' })!;
  assert.equal(hi.value, '200.000000');
  assert.equal(hi.clamped, true);
});

test('duplicate inputs with different outputs are a conflict', () => {
  assert.throws(
    () =>
      new CalibrationTable([
        { x: '1', y: '10' },
        { x: '1.0', y: '11' }, // same x after exact parsing, different y
      ]),
    (e: unknown) => {
      assert.ok(e instanceof CalibrationConflictError);
      assert.equal((e as CalibrationConflictError).x, '1');
      return true;
    },
  );
});

test('duplicate inputs with identical outputs are de-duplicated', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '1', y: '5' },
    { x: '1.0', y: '5.00' },
    { x: '2', y: '10' },
  ]);
  assert.equal(t.domain.from, '0');
  assert.equal(t.domain.to, '2');
  assert.equal(t.forward('1', OPTS)!.value, '5.000000');
});

test('inverse of a rise-then-fall table returns both solutions', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '10', y: '10' },
    { x: '20', y: '0' },
  ]);
  const r = t.inverse('5', OPTS);
  assert.deepEqual(r.points.map((p) => p.value), ['5.000000', '15.000000']);
  assert.equal(r.intervals.length, 0);
  // Peak value has exactly one solution (shared node de-duplicated)
  const peak = t.inverse('10', OPTS);
  assert.deepEqual(peak.points.map((p) => p.value), ['10.000000']);
});

test('inverse of a plateau value returns a continuous interval', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '5', y: '5' },
    { x: '10', y: '5' }, // plateau at y=5
    { x: '15', y: '0' },
  ]);
  const r = t.inverse('5', OPTS);
  assert.equal(r.points.length, 0); // endpoint hits are covered by the interval
  assert.equal(r.intervals.length, 1);
  assert.equal(r.intervals[0].from.value, '5.000000');
  assert.equal(r.intervals[0].to.value, '10.000000');
  // Non-plateau target still yields two discrete solutions
  const two = t.inverse('2.5', OPTS);
  assert.deepEqual(two.points.map((p) => p.value), ['2.500000', '12.500000']);
});

test('inverse quantizes solutions and keeps error bounds', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '1', y: '3' },
  ]);
  const r = t.inverse('1', { scale: 2, rounding: 'half-up', outOfRange: 'error' });
  assert.equal(r.points[0].exact, '1/3');
  assert.equal(r.points[0].value, '0.33');
  assert.equal(r.points[0].exactError, '1/300');
  assert.equal(r.points[0].errorBound, '0.005');
});

test('inverse out-of-range policies are explicit', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '10', y: '10' },
  ]);
  assert.throws(() => t.inverse('11', OPTS), OutOfRangeError);
  const empty = t.inverse('11', { ...OPTS, outOfRange: 'empty' });
  assert.deepEqual(empty.points, []);
  assert.deepEqual(empty.intervals, []);
});

test('huge values never pass through Number', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '18014398509481984', y: '1' }, // 2^54
  ]);
  // 2^53 + 1 is not representable as a double; a Number-based path would
  // silently round it to 2^53 and report exactly 0.5.
  const r = t.forward('9007199254740993', { scale: 60, rounding: 'half-up', outOfRange: 'error' })!;
  assert.equal(
    r.exact,
    '0.500000000000000055511151231257827021181583404541015625',
  );
  assert.notEqual(r.value, '0.5' + '0'.repeat(59));
  const at2pow53 = t.forward('9007199254740992', { scale: 60, rounding: 'half-up', outOfRange: 'error' })!;
  assert.equal(at2pow53.exact, '0.5');
  assert.equal(at2pow53.exactError, '0');
});

test('round-trip: forward then inverse recovers the input within the error bound', () => {
  const t = new CalibrationTable([
    { x: '0', y: '0' },
    { x: '7', y: '11' },
  ]);
  const fwd = t.forward('3', { scale: 4, rounding: 'half-even', outOfRange: 'error' })!;
  const inv = t.inverse(fwd.value, { scale: 4, rounding: 'half-even', outOfRange: 'error' });
  const recovered = Rational.parse(inv.points[0].value);
  const drift = recovered.sub(Rational.parse('3')).abs();
  // y-grid ulp is 1e-4, slope is 11/7, so x drift <= 1e-4 / (11/7) < 1e-4
  assert.ok(drift.compare(Rational.parse('0.0001')) < 0);
});
