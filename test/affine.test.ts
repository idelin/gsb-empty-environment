import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Dec } from '../src/decimal.ts';
import { Rat } from '../src/rational.ts';
import * as Iv from '../src/interval.ts';
import { Affine, AffineCtx } from '../src/affine.ts';
import { checkCertificate, containsRat } from '../src/certificate.ts';

const d = (s: string) => Dec.fromString(s);

test('correlated symbols: x - x is exactly zero', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  const z = Affine.sub(x, x);
  assert.equal(Iv.toString(z.toInterval(ctx.prec)), '[0, 0]');
});

test('linear ops avoid false widening', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  const y = Affine.sub(Affine.add(x, ctx.constant(d('1'))), x); // (x + 1) - x
  assert.equal(Iv.toString(y.toInterval(ctx.prec)), '[1, 1]');
  const naive = Iv.sub(
    Iv.add(Iv.range(d('1'), d('2')), Iv.point(d('1')), 10),
    Iv.range(d('1'), d('2')),
    10,
  );
  assert.equal(Iv.toString(naive), '[0, 2]'); // plain intervals over-approximate
});

test('nonlinear mul emits a conservative remainder certificate', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  const y = Affine.mul(ctx, x, x);
  assert.equal(ctx.certificates.length, 1);
  const cert = ctx.certificates[0];
  assert.equal(cert.op, 'mul');
  assert.equal(checkCertificate(cert).ok, true);
  const iv = y.toInterval(ctx.prec);
  assert.equal(Iv.containsValue(iv, d('1')), true); // true min of t^2 on [1,2]
  assert.equal(Iv.containsValue(iv, d('4')), true); // true max
});

test('reciprocal across zero is rejected', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('-1'), d('2'));
  assert.throws(() => Affine.recip(ctx, x), Iv.ZeroDivisionError);
});

test('division emits verifiable certificates and sound bounds', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  const y = Affine.div(ctx, ctx.constant(d('1')), x);
  assert.equal(ctx.certificates.length >= 1, true);
  for (const c of ctx.certificates) assert.equal(checkCertificate(c).ok, true);
  const iv = y.toInterval(ctx.prec);
  assert.equal(Iv.containsValue(iv, d('0.5')), true);
  assert.equal(Iv.containsValue(iv, d('1')), true);
});

test('any sampled truth falls within the affine bounds', () => {
  const ctx = new AffineCtx(12);
  const x = ctx.variable(d('1'), d('2'));
  const expr = Affine.sub(Affine.mul(ctx, x, x), x); // t^2 - t
  const iv = expr.toInterval(ctx.prec);
  for (let i = 0; i <= 20; i++) {
    const t = Rat.of(20n + BigInt(i), 20n); // 1 + i/20 in [1, 2]
    const truth = Rat.sub(Rat.mul(t, t), t);
    assert.equal(containsRat(iv, truth), true, `sample t=${t.toString()} escaped`);
  }
});

test('sampled truth also holds for division expressions', () => {
  const ctx = new AffineCtx(12);
  const x = ctx.variable(d('1'), d('2'));
  const expr = Affine.div(ctx, Affine.add(x, ctx.constant(d('1'))), x); // (t+1)/t
  const iv = expr.toInterval(ctx.prec);
  for (let i = 0; i <= 20; i++) {
    const t = Rat.of(20n + BigInt(i), 20n);
    const truth = Rat.div(Rat.add(t, Rat.one), t);
    assert.equal(containsRat(iv, truth), true, `sample t=${t.toString()} escaped`);
  }
});
