import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  Rational,
  UnitRegistry,
  chainExact,
  deferredVsStepwise,
  formatRational,
} from '../src/index.ts';

const registry = () =>
  new UnitRegistry(
    [
      { name: 'm', dimension: 'length' },
      { name: 'mm', dimension: 'length' },
      { name: 'km', dimension: 'length' },
      { name: 'K', dimension: 'temperature' },
      { name: 'C', dimension: 'temperature' },
      { name: 'F', dimension: 'temperature' },
      { name: 'u1', dimension: 'demo' },
      { name: 'u2', dimension: 'demo' },
      { name: 'u3', dimension: 'demo' },
    ],
    [
      { from: 'm', to: 'mm', ratio: 1000 },
      { from: 'km', to: 'm', ratio: 1000 },
      { from: 'C', to: 'K', ratio: 1, offset: '273.15' },
      { from: 'F', to: 'C', ratio: '5/9', offset: '-160/9' },
      { from: 'u1', to: 'u2', ratio: '2/3' },
      { from: 'u2', to: 'u3', ratio: '2/3' },
    ],
  );

describe('chainExact', () => {
  test('1 m -> km -> mm is exactly 1000 mm, matching the direct conversion', () => {
    const reg = registry();
    const chain = chainExact(reg, 1, ['m', 'km', 'mm'], 'absolute');
    assert.equal(chain.result.toString(), '1000');
    assert.deepEqual(
      chain.steps.map((s) => `${formatRational(s.value)} ${s.unit}`),
      ['1 m', '0.001 km', '1000 mm'],
    );
    // exact rational reference: direct composed conversion agrees
    assert.ok(reg.convert(1, 'm', 'mm', 'absolute').equals(chain.result));
  });

  test('affine chains compose exactly: 20 C -> K -> F is 68 F', () => {
    const reg = registry();
    const chain = chainExact(reg, 20, ['C', 'K', 'F'], 'absolute');
    assert.equal(formatRational(chain.result), '68');
    assert.equal(formatRational(chain.steps[1].value), '293.15');
  });

  test('delta chains drop offsets at every hop', () => {
    const reg = registry();
    const chain = chainExact(reg, 20, ['C', 'K', 'F'], 'delta');
    assert.equal(formatRational(chain.result), '36');
  });
});

describe('deferredVsStepwise', () => {
  test('quantifies the rounding-strategy difference with exact rationals', () => {
    const reg = registry();
    // 8 u1 = 16/3 u2 = 32/9 u3 (exact reference).
    // deferred:  round(32/9)  = round(3.55..) = 4
    // stepwise:  round(16/3)  = 5, then round(5 * 2/3) = round(10/3) = 3
    const cmp = deferredVsStepwise(reg, 8, ['u1', 'u2', 'u3'], 'absolute', 'half-up');
    assert.equal(cmp.exact.toString(), '32/9');
    assert.equal(cmp.deferred.toString(), '4');
    assert.equal(cmp.stepwise.toString(), '3');
    assert.equal(cmp.difference.toString(), '1');
    // cross-check the exact reference by hand
    assert.ok(cmp.exact.equals(Rational.of(8).mul('2/3').mul('2/3')));
  });

  test('when no rounding is needed the strategies agree', () => {
    const reg = registry();
    const cmp = deferredVsStepwise(reg, 9, ['u1', 'u2', 'u3'], 'absolute', 'half-up');
    assert.equal(cmp.exact.toString(), '4');
    assert.equal(cmp.difference.toString(), '0');
  });
});
