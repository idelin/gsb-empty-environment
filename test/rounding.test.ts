import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';
import { roundTo } from '../src/rounding.ts';
import type { RoundingSpec } from '../src/rounding.ts';

const unit: RoundingSpec = { quantum: Rat.one(), mode: 'floor' };
const spec = (mode: RoundingSpec['mode'], q = '1'): RoundingSpec => ({
  quantum: Rat.parse(q),
  mode,
});

test('every mode is explicit and distinct on ties', () => {
  const half = Rat.of(5n, 2n); // 2.5
  assert.ok(roundTo(half, spec('floor')).eq(Rat.of(2n)));
  assert.ok(roundTo(half, spec('ceil')).eq(Rat.of(3n)));
  assert.ok(roundTo(half, spec('truncate')).eq(Rat.of(2n)));
  assert.ok(roundTo(half, spec('half-up')).eq(Rat.of(3n)));
  assert.ok(roundTo(half, spec('half-down')).eq(Rat.of(2n)));
  assert.ok(roundTo(half, spec('half-even')).eq(Rat.of(2n)));
  assert.ok(roundTo(Rat.of(7n, 2n), spec('half-even')).eq(Rat.of(4n)));
});

test('negative values follow the documented tie direction', () => {
  const negHalf = Rat.of(-5n, 2n); // -2.5
  assert.ok(roundTo(negHalf, spec('floor')).eq(Rat.of(-3n)));
  assert.ok(roundTo(negHalf, spec('ceil')).eq(Rat.of(-2n)));
  assert.ok(roundTo(negHalf, spec('truncate')).eq(Rat.of(-2n)));
  assert.ok(roundTo(negHalf, spec('half-up')).eq(Rat.of(-2n))); // ties toward +inf
  assert.ok(roundTo(negHalf, spec('half-down')).eq(Rat.of(-3n)));
  assert.ok(roundTo(negHalf, spec('half-even')).eq(Rat.of(-2n)));
});

test('non-unit quantum rounds to multiples of the quantum', () => {
  const q = spec('floor', '0.25');
  assert.ok(roundTo(Rat.parse('1.13'), q).eq(Rat.parse('1')));
  assert.ok(roundTo(Rat.parse('1.13'), spec('ceil', '0.25')).eq(Rat.parse('1.25')));
  assert.ok(roundTo(Rat.parse('-1.13'), spec('floor', '0.25')).eq(Rat.parse('-1.25')));
});

test('exact multiples are untouched', () => {
  assert.ok(roundTo(Rat.of(4n), unit).eq(Rat.of(4n)));
  assert.ok(roundTo(Rat.parse('0.5'), spec('half-up', '0.5')).eq(Rat.parse('0.5')));
});
