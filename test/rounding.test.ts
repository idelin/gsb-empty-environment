import test from 'node:test';
import assert from 'node:assert/strict';
import { make, parse, toString } from '../src/rational.ts';
import { preimage, roundTo, type RoundingSpec } from '../src/rounding.ts';
import { contains } from '../src/interval.ts';

const q1 = (mode: RoundingSpec['mode']): RoundingSpec => ({ quantum: parse('1'), mode });

test('floor / ceil / trunc including negatives', () => {
  assert.equal(toString(roundTo(parse('-3/2'), q1('floor'))), '-2');
  assert.equal(toString(roundTo(parse('-3/2'), q1('ceil'))), '-1');
  assert.equal(toString(roundTo(parse('-3/2'), q1('trunc'))), '-1');
  assert.equal(toString(roundTo(parse('3/2'), q1('trunc'))), '1');
});

test('half-up ties toward +inf, half-away ties away from zero', () => {
  assert.equal(toString(roundTo(parse('5/2'), q1('half-up'))), '3');
  assert.equal(toString(roundTo(parse('-5/2'), q1('half-up'))), '-2');
  assert.equal(toString(roundTo(parse('5/2'), q1('half-away-from-zero'))), '3');
  assert.equal(toString(roundTo(parse('-5/2'), q1('half-away-from-zero'))), '-3');
});

test('half-even ties to even multiples', () => {
  assert.equal(toString(roundTo(parse('5/2'), q1('half-even'))), '2');
  assert.equal(toString(roundTo(parse('7/2'), q1('half-even'))), '4');
  assert.equal(toString(roundTo(parse('-5/2'), q1('half-even'))), '-2');
  assert.equal(toString(roundTo(parse('-7/2'), q1('half-even'))), '-4');
});

test('fractional quantum', () => {
  const spec: RoundingSpec = { quantum: parse('0.01'), mode: 'half-up' };
  assert.equal(toString(roundTo(parse('1.005'), spec)), '101/100');
  assert.equal(toString(roundTo(parse('1.004'), spec)), '1');
});

test('preimage intervals are exact and consistent with roundTo on a grid', () => {
  const modes: RoundingSpec['mode'][] = ['floor', 'ceil', 'trunc', 'half-up', 'half-away-from-zero', 'half-even'];
  for (const mode of modes) {
    const spec: RoundingSpec = { quantum: parse('1/2'), mode };
    for (let yk = -4n; yk <= 4n; yk++) {
      const y = make(yk, 2n);
      const iv = preimage(y, spec);
      assert.ok(iv, `preimage exists for multiple of quantum (${mode})`);
      // every grid point inside rounds to y; every grid point outside does not
      for (let k = -12n; k <= 12n; k++) {
        const v = { n: k, d: 4n };
        const inside = contains(iv, v);
        const roundsToY = toString(roundTo(v, spec)) === toString(y);
        assert.equal(inside, roundsToY, `${mode} at ${toString(v)} for y=${toString(y)}`);
      }
    }
    // non-multiple of the quantum has empty preimage
    assert.equal(preimage(parse('1/3'), spec), null);
  }
});
