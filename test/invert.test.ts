import test from 'node:test';
import assert from 'node:assert/strict';
import { make, parse, toString } from '../src/rational.ts';
import { contains, intervalToString, type Interval } from '../src/interval.ts';
import { evaluate } from '../src/evaluate.ts';
import { compile } from '../src/compile.ts';
import { invert } from '../src/invert.ts';
import type { Bracket, CompositeSpec } from '../src/rules.ts';

const B = (
  l: string | null,
  lc: boolean,
  u: string | null,
  uc: boolean,
  rate: string,
  fixed = '0',
  extra: Partial<Bracket> = {},
): Bracket => ({
  lower: { value: l === null ? null : parse(l), closed: lc },
  upper: { value: u === null ? null : parse(u), closed: uc },
  rate: parse(rate),
  fixed: parse(fixed),
  ...extra,
});

const show = (ivs: Interval[]) => ivs.map(intervalToString).join(' ∪ ');

/** Two disjoint platforms at height 5, ramps elsewhere. */
function platformSpec(): CompositeSpec {
  return {
    rules: [
      {
        name: 'w',
        mode: 'whole',
        brackets: [
          B(null, false, '-5', false, '-1'),
          B('-5', true, '0', true, '0', '5'),
          B('0', false, '5', false, '1'),
          B('5', true, '10', true, '0', '5'),
          B('10', false, null, false, '1', '-5'),
        ],
      },
    ],
  };
}

test('platform value yields multiple disjoint intervals', () => {
  const c = compile(platformSpec());
  assert.equal(show(invert(c, parse('5'))), '[-5, 0] ∪ [5, 10]');
});

test('non-platform value yields isolated points; unattainable value yields nothing', () => {
  const c = compile(platformSpec());
  assert.equal(show(invert(c, parse('3'))), '[3]');
  assert.equal(show(invert(c, parse('7'))), '[-7] ∪ [12]');
  assert.equal(show(invert(c, parse('0'))), '');
});

test('final rounding widens the inverse to the rounding preimage', () => {
  const spec: CompositeSpec = {
    rules: [{ name: 'w', mode: 'whole', brackets: [B(null, false, null, false, '1')] }],
    finalRounding: { quantum: parse('1'), mode: 'floor' },
  };
  const c = compile(spec);
  assert.equal(show(invert(c, parse('2'))), '[2, 3)');
  assert.equal(show(invert(c, parse('-2'))), '[-2, -1)');
  // y not a multiple of the quantum: no solutions
  assert.equal(show(invert(c, parse('1/2'))), '');
});

test('segment rounding inverts to exact step intervals on an unbounded domain', () => {
  const spec: CompositeSpec = {
    rules: [
      {
        name: 's',
        mode: 'whole',
        segmentRounding: { quantum: parse('1'), mode: 'floor' },
        brackets: [B(null, false, '0', false, '0'), B('0', true, null, false, '1/2')],
      },
    ],
  };
  const c = compile(spec);
  assert.equal(show(invert(c, parse('2'))), '[4, 6)');
  assert.equal(show(invert(c, parse('0'))), '(-∞, 2)');
});

test('global clamp saturates: inverse includes the saturation ray', () => {
  const spec: CompositeSpec = {
    rules: [{ name: 'w', mode: 'whole', brackets: [B(null, false, null, false, '1')] }],
    max: parse('10'),
  };
  const c = compile(spec);
  assert.equal(show(invert(c, parse('10'))), '[10, +∞)');
  assert.equal(show(invert(c, parse('4'))), '[4]');
});

test('reference check: inverse intervals match direct evaluation on a rational grid', () => {
  const spec: CompositeSpec = {
    rules: [
      {
        name: 'p',
        mode: 'progressive',
        brackets: [B(null, false, '0', false, '0'), B('0', true, '4', false, '1/2'), B('4', true, null, false, '1/4', '1')],
      },
      {
        name: 'w',
        mode: 'whole',
        brackets: [B(null, false, '-2', false, '0', '3'), B('-2', true, '2', true, '0', '1'), B('2', false, null, false, '0', '2')],
      },
    ],
    max: parse('6'),
    finalRounding: { quantum: parse('1/2'), mode: 'floor' },
  };
  const c = compile(spec);
  // collect candidate outputs from the grid, then verify each inverse exactly
  const ys = new Map<string, ReturnType<typeof parse>>();
  for (let k = -32; k <= 64; k++) {
    const y = evaluate(spec, make(BigInt(k), 4n)).output;
    ys.set(toString(y), y);
  }
  for (const y of ys.values()) {
    const sols = invert(c, y);
    for (let k = -32; k <= 64; k++) {
      const x = make(BigInt(k), 4n);
      const inSolution = sols.some((iv) => contains(iv, x));
      const matches = toString(evaluate(spec, x).output) === toString(y);
      assert.equal(inSolution, matches, `y=${toString(y)} x=${toString(x)}`);
    }
  }
});

test('inverse of a progressive tax-like rule is a single exact interval', () => {
  const spec: CompositeSpec = {
    rules: [
      {
        name: 'p',
        mode: 'progressive',
        brackets: [B(null, false, '0', false, '0'), B('0', true, '10', false, '1/10'), B('10', true, null, false, '1/5')],
      },
    ],
  };
  const c = compile(spec);
  // f(x) = 2 ⟺ 1 + (x-10)/5 = 2 ⟺ x = 15
  assert.equal(show(invert(c, parse('2'))), '[15]');
  // f(x) = 1/2 ⟺ x/10 = 1/2 ⟺ x = 5
  assert.equal(show(invert(c, parse('1/2'))), '[5]');
  // negative output is unattainable
  assert.equal(show(invert(c, parse('-1'))), '');
});
