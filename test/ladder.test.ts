import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';
import { compileLadder, evaluate } from '../src/ladder.ts';
import type { LadderRule } from '../src/ladder.ts';

const r = (s: string) => Rat.parse(s);

function progressiveRule(extra: Partial<LadderRule> = {}): LadderRule {
  return {
    name: 'prog',
    mode: 'progressive',
    brackets: [
      { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: true, rate: r('0.1') },
      { lower: r('100'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.2') },
    ],
    ...extra,
  };
}

function flatRule(extra: Partial<LadderRule> = {}): LadderRule {
  return {
    name: 'flat',
    mode: 'flat',
    brackets: [
      { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: true, rate: r('0.1') },
      { lower: r('100'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.2') },
    ],
    ...extra,
  };
}

test('progressive vs flat differ when crossing a tier', () => {
  const prog = compileLadder({ rules: [progressiveRule()] });
  const flat = compileLadder({ rules: [flatRule()] });
  const x = r('150');
  // progressive: 100*0.1 + 50*0.2 = 20; flat: 150*0.2 = 30
  assert.ok(evaluate(prog, x).total.eq(r('20')));
  assert.ok(evaluate(flat, x).total.eq(r('30')));
});

test('exact threshold is never double counted', () => {
  const prog = compileLadder({ rules: [progressiveRule()] });
  const ev = evaluate(prog, r('100'));
  assert.ok(ev.total.eq(r('10')));
  assert.equal(ev.rules[0].segments.length, 1); // second bracket contributes nothing

  const flat = compileLadder({ rules: [flatRule()] });
  const evF = evaluate(flat, r('100'));
  assert.equal(evF.rules[0].matchedBracket, 0); // closed upper side owns the threshold
  assert.ok(evF.total.eq(r('10')));
});

test('compiler rejects a threshold closed on both sides', () => {
  assert.throws(() =>
    compileLadder({
      rules: [
        flatRule({
          brackets: [
            { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: true, rate: r('0.1') },
            { lower: r('100'), lowerClosed: true, upper: null, upperClosed: false, rate: r('0.2') },
          ],
        }),
      ],
    }),
  );
});

test('endpoint openness decides bracket ownership', () => {
  const openUpper = compileLadder({
    rules: [
      flatRule({
        brackets: [
          { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: false, rate: r('0.1') },
          { lower: r('100'), lowerClosed: true, upper: null, upperClosed: false, rate: r('0.2') },
        ],
      }),
    ],
  });
  const ev = evaluate(openUpper, r('100'));
  assert.equal(ev.rules[0].matchedBracket, 1);
  assert.ok(ev.total.eq(r('20')));
});

test('min, max and discount apply after rounding, in that order', () => {
  const rule = flatRule({
    brackets: [
      { lower: null, lowerClosed: false, upper: null, upperClosed: false, rate: r('0.1') },
    ],
    discount: r('0.5'),
    min: r('3'),
    max: r('40'),
  });
  const ladder = compileLadder({ rules: [rule] });
  assert.ok(evaluate(ladder, r('1000')).total.eq(r('40'))); // 100*0.5 capped
  assert.ok(evaluate(ladder, r('10')).total.eq(r('3')));    // 1*0.5 lifted to min
  assert.ok(evaluate(ladder, r('200')).total.eq(r('10')));  // 20*0.5 untouched
  const ev = evaluate(ladder, r('1000'));
  assert.equal(ev.rules[0].clamped, true);
});

test('per-segment rounding and rule-total rounding are not interchangeable', () => {
  const brackets = [
    { lower: r('0'), lowerClosed: true, upper: r('6'), upperClosed: true, rate: r('0.15') },
    { lower: r('6'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.15') },
  ];
  const perSegment = compileLadder({
    rules: [{ name: 'a', mode: 'progressive', brackets, rounding: { quantum: r('1'), mode: 'floor', at: 'segment' } }],
  });
  const perTotal = compileLadder({
    rules: [{ name: 'b', mode: 'progressive', brackets, rounding: { quantum: r('1'), mode: 'floor', at: 'rule-total' } }],
  });
  // x=12: raw segments 0.9 and 0.9
  assert.ok(evaluate(perSegment, r('12')).total.eq(r('0'))); // floor(0.9)+floor(0.9)
  assert.ok(evaluate(perTotal, r('12')).total.eq(r('1')));   // floor(1.8)
});

test('final rounding is independent of rule-level rounding', () => {
  const mkRule = (name: string, rounding?: LadderRule['rounding']): LadderRule => ({
    name,
    mode: 'flat',
    brackets: [{ lower: null, lowerClosed: false, upper: null, upperClosed: false, rate: r('0.6') }],
    rounding,
  });
  const ruleRounding = compileLadder({
    rules: [mkRule('a', { quantum: r('1'), mode: 'floor', at: 'rule-total' }), mkRule('b', { quantum: r('1'), mode: 'floor', at: 'rule-total' })],
  });
  const finalRounding = compileLadder({
    rules: [mkRule('a'), mkRule('b')],
    finalRounding: { quantum: r('1'), mode: 'floor' },
  });
  // x=1: each rule yields 0.6
  assert.ok(evaluate(ruleRounding, r('1')).total.eq(r('0'))); // floor each, then sum
  assert.ok(evaluate(finalRounding, r('1')).total.eq(r('1'))); // sum 1.2, then floor
});

test('negative inputs flow through progressive brackets', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'neg',
        mode: 'progressive',
        brackets: [
          { lower: r('-100'), lowerClosed: true, upper: r('0'), upperClosed: true, rate: r('0.1') },
          { lower: r('0'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.2') },
        ],
      },
    ],
  });
  assert.ok(evaluate(ladder, r('-50')).total.eq(r('5')));  // 50 * 0.1
  assert.ok(evaluate(ladder, r('50')).total.eq(r('20')));  // 10 + 50*0.2
  assert.ok(evaluate(ladder, r('-150')).total.eq(r('0'))); // below the ladder
});

test('evidence lists every touched segment with its contribution', () => {
  const ladder = compileLadder({ rules: [progressiveRule()] });
  const ev = evaluate(ladder, r('150'));
  const segs = ev.rules[0].segments;
  assert.equal(segs.length, 2);
  assert.ok(segs[0].inputPortion.eq(r('100')));
  assert.ok(segs[0].raw.eq(r('10')));
  assert.ok(segs[1].inputPortion.eq(r('50')));
  assert.ok(segs[1].raw.eq(r('10')));
  assert.ok(ev.rules[0].subtotalBeforeRounding.eq(r('20')));
});

test('huge inputs stay exact (no Number transit)', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'big',
        mode: 'flat',
        brackets: [{ lower: null, lowerClosed: false, upper: null, upperClosed: false, rate: r('0.1') }],
      },
    ],
  });
  const x = Rat.of(2n ** 80n);
  const ev = evaluate(ladder, x);
  assert.ok(ev.total.eq(Rat.of(2n ** 80n, 10n)));
});
