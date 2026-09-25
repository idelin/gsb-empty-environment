import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SRGB, LINEAR, gammaTransfer } from '../src/index.ts';

test('sRGB transfer roundtrips over a wide range', () => {
  const values = [-2, -1, -0.5, -0.2, -0.04045, -0.0031308, 0, 0.0031308, 0.04045, 0.2, 0.5, 1, 2, 10];
  for (const v of values) {
    const enc = SRGB.encode(v);
    assert.ok(Number.isFinite(enc), `encode(${v}) must be finite`);
    const back = SRGB.decode(enc);
    assert.ok(Math.abs(back - v) < 1e-12, `roundtrip(${v}) = ${back}`);
  }
});

test('negative linear components follow the sign-mirrored definition, never NaN', () => {
  for (const v of [-0.001, -0.01, -0.2, -0.9, -5]) {
    const enc = SRGB.encode(v);
    assert.ok(!Number.isNaN(enc), `encode(${v}) must not be NaN`);
    assert.equal(Math.sign(enc), -1);
    assert.ok(Math.abs(enc + SRGB.encode(-v)) < 1e-15, 'sign symmetry: f(-x) = -f(x)');
  }
  assert.equal(SRGB.encode(0), 0);
});

test('gamma transfer is bijective with signed pow', () => {
  const g = gammaTransfer(2.2);
  for (const v of [-0.75, -0.1, 0, 0.1, 0.75, 3]) {
    assert.ok(Math.abs(g.decode(g.encode(v)) - v) < 1e-12);
  }
  assert.throws(() => gammaTransfer(0));
  assert.throws(() => gammaTransfer(Number.NaN));
});

test('linear transfer is identity', () => {
  assert.equal(LINEAR.encode(-3.5), -3.5);
  assert.equal(LINEAR.decode(7.25), 7.25);
});
