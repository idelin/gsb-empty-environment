import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import type { IntervalOpCert } from '../src/interval.ts';
import { div, interval, mul } from '../src/interval.ts';
import { AffineForm } from '../src/affine.ts';
import { Rational } from '../src/rational.ts';
import {
  sampledTruthCheck, searchAffine, searchAffineRecip, searchIntervalOp,
} from '../src/search.ts';
import type { Arith } from '../src/search.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));

test('no counterexample exists for honest certificates', () => {
  const certs: IntervalOpCert[] = [];
  mul(iv('-1.5', '2'), iv('0.25', '0.5'), 6, certs);
  div(iv('1', '2'), iv('-1', '3'), 6, 'split', certs);
  for (const c of certs) assert.equal(searchIntervalOp(c), null);
});

test('search finds an exact rational counterexample for a wrong bound', () => {
  const bad: IntervalOpCert = {
    kind: 'interval-op', op: 'mul', lhs: iv('0', '2'), rhs: iv('0', '2'), scale: 2,
    segments: [iv('0', '3.99')], // true hi is 4
  };
  const cex = searchIntervalOp(bad);
  assert.ok(cex !== null);
  // the reported value is an exact rational and truly outside the segment
  const v = Rational.of(4n);
  assert.equal(cex.value, v.toString());
  assert.equal(cex.points.join(','), '2,2');
});

test('search finds counterexample for tampered affine remainders', () => {
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  const { cert } = x.mul(x, 10);
  assert.equal(searchAffine(cert), null);
  const tampered = { ...cert, remainder: d('0.005') }; // true need: 0.01
  const cex = searchAffine(tampered);
  assert.ok(cex !== null);
});

test('search finds counterexample for tampered recip delta', () => {
  const x = AffineForm.fromInterval(iv('2', '4'));
  const { cert } = x.recip(6);
  assert.equal(searchAffineRecip(cert), null);
  const tampered = { ...cert, delta: d('0.01') }; // true max |g| is ~0.0429
  assert.ok(searchAffineRecip(tampered) !== null);
});

test('every sampled truth lies within the affine output bounds', () => {
  // correlated expression: (x + y) * (x - y) keeps shared noise symbols
  const f = <T>(ops: Arith<T>, [x, y]: T[]): T =>
    ops.mul(ops.add(x, y), ops.sub(x, y));
  const r = sampledTruthCheck(f, [iv('0.9', '1.1'), iv('1.9', '2.1')], 8);
  assert.equal(r.violations.length, 0);
  assert.ok(r.checked > 0);
});

test('x - x collapses to [0, 0] and sampling agrees', () => {
  const f = <T>(ops: Arith<T>, [x]: T[]): T => ops.sub(x, x);
  const r = sampledTruthCheck(f, [iv('0.9', '1.1')], 8);
  assert.deepEqual(
    r.output.kind === 'bounded' && r.output.lo?.toString(),
    '0',
  );
  assert.equal(r.violations.length, 0);
});

test('nonlinear chain with division stays sound under sampling', () => {
  const f = <T>(ops: Arith<T>, [x]: T[]): T =>
    ops.div(ops.mul(x, x), ops.add(x, ops.constant('1')), );
  const r = sampledTruthCheck(f, [iv('0.5', '1.5')], 8);
  assert.equal(r.violations.length, 0);
  assert.ok(r.certs.length >= 2); // recip + mul certificates were emitted
});
