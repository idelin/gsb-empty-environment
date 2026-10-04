import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Dec } from '../src/decimal.ts';
import * as Iv from '../src/interval.ts';
import { Affine, AffineCtx } from '../src/affine.ts';
import { checkBinaryOp, checkCertificate } from '../src/certificate.ts';

const d = (s: string) => Dec.fromString(s);
const P = 6;

test('a valid interval op passes the independent check', () => {
  const a = Iv.range(d('1.5'), d('2.5'));
  const b = Iv.range(d('0.5'), d('1.5'));
  const r = Iv.mul(a, b, P);
  const chk = checkBinaryOp('mul', a, b, r, P);
  assert.equal(chk.ok, true);
  assert.equal(chk.counterexample, null);
});

test('a shrunk result fails containment and yields a rational counterexample', () => {
  const a = Iv.range(d('1'), d('2'));
  const b = Iv.range(d('1'), d('2'));
  const bad = Iv.range(d('1.5'), d('4')); // lower bound too high
  const chk = checkBinaryOp('mul', a, b, bad, P);
  assert.equal(chk.loOutward, false);
  assert.equal(chk.ok, false);
  assert.ok(chk.counterexample);
});

test('an over-precise result fails the precision check', () => {
  const a = Iv.point(d('1'));
  const b = Iv.point(d('3'));
  const r = Iv.div(a, b, 20); // 20 significant digits
  const chk = checkBinaryOp('div', a, b, r, 6);
  assert.equal(chk.withinPrecision, false);
  assert.equal(chk.ok, false);
});

test('a tampered remainder certificate is rejected', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  Affine.mul(ctx, x, x);
  const cert = ctx.certificates[0];
  assert.equal(checkCertificate(cert).ok, true);
  if (cert.op !== 'mul') throw new Error('unexpected certificate');
  const tampered = { ...cert, remainder: d('0.01') };
  assert.equal(checkCertificate(tampered).ok, false);
});

test('a tampered reciprocal certificate is rejected', () => {
  const ctx = new AffineCtx(10);
  const x = ctx.variable(d('1'), d('2'));
  Affine.recip(ctx, x);
  const cert = ctx.certificates[0];
  assert.equal(checkCertificate(cert).ok, true);
  const tampered = { ...cert, remainder: d('0.000001') };
  assert.equal(checkCertificate(tampered).ok, false);
});
