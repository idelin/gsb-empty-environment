import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import type { IntervalOpCert } from '../src/interval.ts';
import { add, div, interval, mul } from '../src/interval.ts';
import { AffineForm } from '../src/affine.ts';
import { verify, verifyAll, verifyIntervalOp } from '../src/verify.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));

test('checker accepts certificates emitted by the library', () => {
  const certs: IntervalOpCert[] = [];
  add(iv('0.1', '0.2'), iv('0.3', '0.4'), 6, certs);
  mul(iv('-1.5', '2'), iv('0.25', '0.5'), 6, certs);
  div(iv('1', '2'), iv('3', '7'), 6, 'reject', certs);
  div(iv('1', '2'), iv('-1', '3'), 6, 'split', certs); // two-ray certificate
  assert.equal(certs.length, 4);
  assert.ok(verifyAll(certs).ok);
});

test('checker rejects a lower bound rounded the wrong way', () => {
  // true range of [1,1]/[3,3] is [1/3, 1/3]; lo = 0.3334 rounds UP (wrong direction)
  const bad: IntervalOpCert = {
    kind: 'interval-op', op: 'div', lhs: iv('1', '1'), rhs: iv('3', '3'), scale: 4,
    segments: [iv('0.3334', '0.3334')],
  };
  const r = verifyIntervalOp(bad);
  assert.ok(!r.ok);
  assert.match(r.reason ?? '', /not contained|direction/);
});

test('checker rejects an upper bound rounded the wrong way', () => {
  const bad: IntervalOpCert = {
    kind: 'interval-op', op: 'add', lhs: iv('0.11', '0.22'), rhs: iv('0.33', '0.44'), scale: 2,
    segments: [iv('0.44', '0.65')], // true hi is 0.66
  };
  assert.ok(!verifyIntervalOp(bad).ok);
});

test('checker rejects missing split segments', () => {
  const bad: IntervalOpCert = {
    kind: 'interval-op', op: 'div', lhs: iv('1', '2'), rhs: iv('-1', '3'), scale: 6,
    segments: [interval(null, d('-1'))], // positive ray missing
  };
  assert.ok(!verifyIntervalOp(bad).ok);
});

test('checker accepts a conservative (wider than necessary) result', () => {
  const wide: IntervalOpCert = {
    kind: 'interval-op', op: 'mul', lhs: iv('1', '2'), rhs: iv('1', '2'), scale: 2,
    segments: [iv('0.5', '5')], // contains true [1, 4]
  };
  assert.ok(verifyIntervalOp(wide).ok);
});

test('checker rejects tampered affine-mul remainder', () => {
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  const y = AffineForm.fromInterval(iv('0.9', '1.1'));
  const { cert } = x.mul(y, 10);
  assert.ok(verify(cert).ok);
  const tampered = { ...cert, remainder: d('0.001') }; // true bound is 0.01
  const r = verify(tampered);
  assert.ok(!r.ok);
  assert.match(r.reason ?? '', /remainder/);
});

test('checker rejects tampered affine-mul linear coefficient', () => {
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  const y = AffineForm.fromInterval(iv('1.9', '2.1'));
  const { cert } = x.mul(y, 10);
  const terms = cert.resultTerms.map(([k, c]) => [k, k === cert.symbol ? c : c.add(d('0.01'))] as [number, Decimal]);
  assert.ok(!verify({ ...cert, resultTerms: terms }).ok);
});

test('checker rejects tampered affine-recip delta via rigorous refinement', () => {
  const x = AffineForm.fromInterval(iv('2', '4'));
  const { cert } = x.recip(6);
  assert.ok(verify(cert).ok);
  // true max |1/x - alpha*x - zeta| on [2,4] is ~0.0429; 0.01 cannot cover it
  const smallDelta = d('0.01');
  const tampered = {
    ...cert,
    delta: smallDelta,
    resultTerms: cert.resultTerms.map(([k, c]) => [k, k === cert.symbol ? smallDelta : c] as [number, Decimal]),
  };
  const r = verify(tampered);
  assert.ok(!r.ok);
  assert.match(r.reason ?? '', /delta/);
});

test('checker rejects affine-recip certificate over a zero-crossing range', () => {
  const x = AffineForm.fromInterval(iv('2', '4'));
  const { cert } = x.recip(6);
  const bad = { ...cert, lo: d('-1') };
  assert.ok(!verify(bad).ok);
});
