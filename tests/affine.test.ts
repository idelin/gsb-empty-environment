import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { interval, show, sub as isub, ZeroCrossingError } from '../src/interval.ts';
import { AffineForm } from '../src/affine.ts';
import { verifyAffine } from '../src/verify.ts';
import { searchAffine } from '../src/search.ts';

const d = (s: string) => Decimal.parse(s);
const iv = (lo: string, hi: string) => interval(d(lo), d(hi));

test('repeated uncertain input keeps its symbol: x - x is exactly zero', () => {
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  const diff = x.sub(x);
  assert.equal(show(diff.toInterval(10)), '[0, 0]');
  // naive interval subtraction widens falsely
  assert.equal(show(isub(iv('0.9', '1.1'), iv('0.9', '1.1'), 10)), '[-0.2000000000, 0.2000000000]');
  // 2x - x - x also cancels exactly
  const two = AffineForm.constant(d('2'));
  assert.equal(show(two.mul(x, 10).form.sub(x).sub(x).toInterval(10)), '[0, 0]');
});

test('linear combinations do not widen; independent inputs stay independent', () => {
  const x = AffineForm.fromInterval(iv('0', '1'));
  const y = AffineForm.fromInterval(iv('0', '1'));
  assert.equal(show(x.add(y).sub(y).toInterval(10)), '[0, 1.0000000000]'); // y cancels
  assert.equal(show(x.sub(y).toInterval(10)), '[-1.0000000000, 1.0000000000]'); // independent
});

test('negative scaling flips coefficients and therefore the bounds', () => {
  const x = AffineForm.fromInterval(iv('-2', '4')); // center 1, radius 3
  const flipped = x.scaleBy(d('-1'));
  assert.equal(show(flipped.toInterval(10)), '[-4.0000000000, 2.0000000000]');
  const twice = x.scaleBy(d('-2'));
  assert.equal(show(twice.toInterval(10)), '[-8.0000000000, 4.0000000000]');
});

test('affine product keeps correlation and certifies its remainder', () => {
  const x = AffineForm.fromInterval(iv('0.9', '1.1'));
  const { form: square, cert } = x.mul(x, 10);
  // center 1, linear coeff 2*1*0.1 = 0.2, remainder 0.01 -> range [0.79, 1.21]
  assert.equal(show(square.toInterval(10)), '[0.7900000000, 1.2100000000]');
  assert.ok(verifyAffine(cert).ok);
  assert.equal(searchAffine(cert), null);
  // reusing the computed square keeps correlation: x^2 - x^2 = 0
  assert.equal(show(square.sub(square).toInterval(10)), '[0, 0]');
});

test('reciprocal rejects zero-crossing ranges and certifies otherwise', () => {
  const bad = AffineForm.fromInterval(iv('-0.5', '1'));
  assert.throws(() => bad.recip(6), ZeroCrossingError);
  const x = AffineForm.fromInterval(iv('2', '4'));
  const { form, cert } = x.recip(6);
  assert.ok(verifyAffine(cert).ok);
  assert.equal(searchAffine(cert), null);
  // true range of 1/x is [0.25, 0.5]; affine range must contain it
  const r = form.rangeExact();
  assert.ok(r.lo.cmp(d('0.25')) <= 0);
  assert.ok(r.hi.cmp(d('0.5')) >= 0);
});

test('affine division produces two verifiable certificates', () => {
  const x = AffineForm.fromInterval(iv('1', '2'));
  const y = AffineForm.fromInterval(iv('2', '4'));
  const { form, certs } = x.div(y, 8);
  assert.equal(certs.length, 2);
  for (const c of certs) {
    assert.ok(verifyAffine(c).ok);
    assert.equal(searchAffine(c), null);
  }
  const r = form.rangeExact();
  assert.ok(r.lo.cmp(d('0.25')) <= 0); // true range [0.25, 1]
  assert.ok(r.hi.cmp(d('1')) >= 0);
});

test('fromInterval rejects unbounded inputs', () => {
  assert.throws(() => AffineForm.fromInterval(interval(null, d('1'))), RangeError);
});
