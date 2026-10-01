import test from 'node:test';
import assert from 'node:assert/strict';
import { parse, toDisplayString, toString } from '../src/rational.ts';
import { evaluate } from '../src/evaluate.ts';
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

test('progressive vs whole differ across a tier boundary', () => {
  const brackets = [B(null, false, '0', false, '0'), B('0', true, '10', false, '1/10'), B('10', true, null, false, '1/5')];
  const progressive: CompositeSpec = { rules: [{ name: 'p', mode: 'progressive', brackets }] };
  const whole: CompositeSpec = { rules: [{ name: 'w', mode: 'whole', brackets }] };
  // x = 20: progressive = 10*0.1 + 10*0.2 = 3; whole = 20*0.2 = 4
  assert.equal(toString(evaluate(progressive, parse('20')).output), '3');
  assert.equal(toString(evaluate(whole, parse('20')).output), '4');
});

test('threshold is counted exactly once (no double count, no gap)', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'w', mode: 'whole', brackets: [B(null, false, '0', true, '1'), B('0', false, null, false, '2')] },
    ],
  };
  const trace = evaluate(spec, parse('0'));
  assert.equal(trace.rules[0].segments.length, 1); // exactly one bracket owns x=0
  assert.equal(trace.rules[0].segments[0].bracketIndex, 0); // the closed side (-inf, 0]
  assert.equal(toString(trace.output), '0');
  // invalid partitions are rejected
  assert.throws(() =>
    evaluate(
      { rules: [{ name: 'bad', mode: 'whole', brackets: [B(null, false, '0', true, '1'), B('0', true, null, false, '2')] }] },
      parse('0'),
    ),
  );
  assert.throws(() =>
    evaluate(
      { rules: [{ name: 'bad', mode: 'whole', brackets: [B(null, false, '0', false, '1'), B('0', false, null, false, '2')] }] },
      parse('0'),
    ),
  );
});

test('progressive is continuous at the threshold when fixed fees are zero', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'p', mode: 'progressive', brackets: [B(null, false, '0', false, '0'), B('0', true, '10', false, '1/10'), B('10', true, null, false, '1/5')] },
    ],
  };
  assert.equal(toString(evaluate(spec, parse('10')).output), '1');
  assert.equal(toDisplayString(evaluate(spec, parse('10.000001')).output), '1.0000002');
  assert.equal(toDisplayString(evaluate(spec, parse('9.999999')).output), '0.9999999');
});

test('per-bracket min/max clamp the segment contribution', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'p', mode: 'progressive', brackets: [B(null, false, '0', false, '0'), B('0', true, null, false, '1/10', '0', { min: parse('5'), max: parse('8') })] },
    ],
  };
  assert.equal(toString(evaluate(spec, parse('20')).output), '5'); // raw 2 clamped up to 5
  assert.equal(toString(evaluate(spec, parse('100')).output), '8'); // raw 10 clamped down to 8
  assert.equal(toString(evaluate(spec, parse('60')).output), '6'); // raw 6 untouched
});

test('discount scales the rule subtotal; rules stack', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'a', mode: 'whole', discount: parse('1/2'), brackets: [B(null, false, null, false, '1/10')] },
      { name: 'b', mode: 'whole', brackets: [B(null, false, null, false, '0', '3')] },
    ],
  };
  // x=40: a: 4 * 1/2 = 2; b: 3; total 5
  assert.equal(toString(evaluate(spec, parse('40')).output), '5');
});

test('global min/max clamp the summed total', () => {
  const spec: CompositeSpec = {
    rules: [{ name: 'a', mode: 'whole', brackets: [B(null, false, null, false, '1')] }],
    min: parse('0'),
    max: parse('10'),
  };
  assert.equal(toString(evaluate(spec, parse('-3')).output), '0');
  assert.equal(toString(evaluate(spec, parse('42')).output), '10');
  assert.equal(toString(evaluate(spec, parse('7')).output), '7');
});

test('negative inputs across negative brackets', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'w', mode: 'whole', brackets: [B(null, false, '-10', false, '1'), B('-10', true, '0', false, '2'), B('0', true, null, false, '3')] },
    ],
  };
  assert.equal(toString(evaluate(spec, parse('-20')).output), '-20');
  assert.equal(toString(evaluate(spec, parse('-5')).output), '-10');
  assert.equal(toString(evaluate(spec, parse('5')).output), '15');
});

test('segment rounding and final rounding are independent and not interchangeable', () => {
  const brackets = [B(null, false, '0', false, '0'), B('0', true, '1', false, '1/2'), B('1', true, '2', false, '1/2'), B('2', true, null, false, '1/2')];
  const segFirst: CompositeSpec = {
    rules: [{ name: 'p', mode: 'progressive', segmentRounding: { quantum: parse('1'), mode: 'floor' }, brackets }],
  };
  const finalOnly: CompositeSpec = {
    rules: [{ name: 'p', mode: 'progressive', brackets }],
    finalRounding: { quantum: parse('1'), mode: 'floor' },
  };
  // x = 5/2: per-segment floor gives 0+0+0 = 0; floor of the total 5/4 gives 1
  const x = parse('5/2');
  assert.equal(toString(evaluate(segFirst, x).output), '0');
  assert.equal(toString(evaluate(finalOnly, x).output), '1');
});

test('huge input stays exact (no Number detour)', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'p', mode: 'progressive', brackets: [B(null, false, '0', false, '0'), B('0', true, null, false, '1/3')] },
    ],
  };
  const trace = evaluate(spec, parse('123456789012345678901234567890'));
  assert.equal(toString(trace.output), '41152263004115226300411522630');
});

test('evidence trace lists every contributing segment', () => {
  const spec: CompositeSpec = {
    rules: [
      { name: 'p', mode: 'progressive', brackets: [B(null, false, '0', false, '0'), B('0', true, '10', false, '1/10'), B('10', true, null, false, '1/5')] },
    ],
  };
  const trace = evaluate(spec, parse('25'));
  const segs = trace.rules[0].segments;
  assert.equal(segs.length, 2);
  assert.equal(toString(segs[0].overlap!), '10');
  assert.equal(toString(segs[0].raw), '1');
  assert.equal(toString(segs[1].overlap!), '15');
  assert.equal(toString(segs[1].raw), '3');
  assert.equal(toString(trace.output), '4');
});
