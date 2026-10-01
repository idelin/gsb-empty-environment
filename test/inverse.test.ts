import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';
import { compileLadder, evaluate } from '../src/ladder.ts';
import type { LadderRule } from '../src/ladder.ts';
import { invert, intervalContains } from '../src/inverse.ts';
import type { Interval } from '../src/inverse.ts';

const r = (s: string) => Rat.parse(s);
const win = (from: string, to: string) => ({ from: r(from), to: r(to) });

function progressiveLadder(): LadderRule {
  return {
    name: 'prog',
    mode: 'progressive',
    brackets: [
      { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: true, rate: r('0.1') },
      { lower: r('100'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.2') },
    ],
  };
}

function feeLadder(): LadderRule {
  return {
    name: 'fee',
    mode: 'flat',
    brackets: [
      { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: false, rate: r('0'), offset: r('5') },
      { lower: r('100'), lowerClosed: true, upper: r('200'), upperClosed: false, rate: r('0'), offset: r('3') },
      { lower: r('200'), lowerClosed: true, upper: r('300'), upperClosed: true, rate: r('0'), offset: r('5') },
    ],
  };
}

test('invert a strictly increasing progressive ladder to a point', () => {
  const ladder = compileLadder({ rules: [progressiveLadder()] });
  const ivs = invert(ladder, r('15'), win('0', '500'));
  assert.equal(ivs.length, 1);
  assert.ok(ivs[0].lower.eq(r('125')));
  assert.ok(ivs[0].upper.eq(r('125')));
  assert.ok(ivs[0].lowerClosed && ivs[0].upperClosed);
});

test('flat ladder inverts to plateaus with explicit endpoints', () => {
  const ladder = compileLadder({ rules: [feeLadder()] });
  const ivs = invert(ladder, r('5'), win('0', '300'));
  assert.equal(ivs.length, 2);
  // [0, 100) and [200, 300]
  assert.ok(ivs[0].lower.eq(r('0')) && ivs[0].lowerClosed);
  assert.ok(ivs[0].upper.eq(r('100')) && !ivs[0].upperClosed);
  assert.ok(ivs[1].lower.eq(r('200')) && ivs[1].lowerClosed);
  assert.ok(ivs[1].upper.eq(r('300')) && ivs[1].upperClosed);
});

test('per-segment rounding creates genuine plateau intervals', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'stair',
        mode: 'progressive',
        brackets: [{ lower: r('0'), lowerClosed: true, upper: null, upperClosed: false, rate: r('1') }],
        rounding: { quantum: r('1'), mode: 'floor', at: 'segment' },
      },
    ],
  });
  const ivs = invert(ladder, r('3'), win('0', '10'));
  assert.equal(ivs.length, 1);
  assert.ok(ivs[0].lower.eq(r('3')) && ivs[0].lowerClosed);
  assert.ok(ivs[0].upper.eq(r('4')) && !ivs[0].upperClosed);
});

test('negative inputs are reachable by inversion', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'neg',
        mode: 'flat',
        brackets: [
          { lower: r('-100'), lowerClosed: true, upper: r('0'), upperClosed: false, rate: r('0'), offset: r('-7') },
          { lower: r('0'), lowerClosed: true, upper: null, upperClosed: false, rate: r('0'), offset: r('2') },
        ],
      },
    ],
  });
  const ivs = invert(ladder, r('-7'), win('-100', '100'));
  assert.equal(ivs.length, 1);
  assert.ok(ivs[0].lower.eq(r('-100')) && ivs[0].lowerClosed);
  assert.ok(ivs[0].upper.eq(r('0')) && !ivs[0].upperClosed);
});

test('no solution yields an empty list', () => {
  const ladder = compileLadder({ rules: [feeLadder()] });
  assert.deepEqual(invert(ladder, r('999'), win('0', '300')), []);
});

test('inversion respects rule-total rounding, discount and clamps', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'mix',
        mode: 'progressive',
        brackets: [{ lower: r('0'), lowerClosed: true, upper: null, upperClosed: false, rate: r('0.3') }],
        rounding: { quantum: r('1'), mode: 'floor', at: 'rule-total' },
        discount: r('2'),
        max: r('10'),
      },
    ],
  });
  // output = min(2*floor(0.3x), 10); target 4 -> floor(0.3x)=2 -> x in [20/3, 10)
  const ivs = invert(ladder, r('4'), win('0', '100'));
  assert.equal(ivs.length, 1);
  assert.ok(ivs[0].lower.eq(r('20/3')) && ivs[0].lowerClosed);
  assert.ok(ivs[0].upper.eq(r('10')) && !ivs[0].upperClosed);
  // clamp plateau: target 10 -> 2*floor(0.3x) >= 10 -> x >= 50/3
  const capped = invert(ladder, r('10'), win('0', '100'));
  assert.equal(capped.length, 1);
  assert.ok(capped[0].lower.eq(r('50/3')) && capped[0].lowerClosed);
  assert.ok(capped[0].upper.eq(r('100')) && capped[0].upperClosed);
});

test('final rounding plateaus are part of the inverse', () => {
  const ladder = compileLadder({
    rules: [
      {
        name: 'lin',
        mode: 'flat',
        brackets: [{ lower: null, lowerClosed: false, upper: null, upperClosed: false, rate: r('0.5') }],
      },
    ],
    finalRounding: { quantum: r('1'), mode: 'half-up' },
  });
  // half-up(0.5x) = 2  <=>  0.5x in [1.5, 2.5)  <=>  x in [3, 5)
  const ivs = invert(ladder, r('2'), win('0', '20'));
  assert.equal(ivs.length, 1);
  assert.ok(ivs[0].lower.eq(r('3')) && ivs[0].lowerClosed);
  assert.ok(ivs[0].upper.eq(r('5')) && !ivs[0].upperClosed);
});

test('every returned interval really maps to the target (sweep)', () => {
  const ladder = compileLadder({
    rules: [progressiveLadder(), feeLadder()],
    finalRounding: { quantum: r('0.5'), mode: 'floor' },
  });
  for (const target of ['5', '7.5', '12', '30']) {
    const ivs = invert(ladder, r(target), win('0', '300'));
    assert.ok(ivs.length > 0, `target ${target} reachable`);
    for (const iv of ivs) {
      const samples = sampleInterval(iv);
      for (const s of samples) {
        assert.ok(
          evaluate(ladder, s).total.eq(r(target)),
          `x=${s} should map to ${target}`,
        );
      }
    }
  }
});

function sampleInterval(iv: Interval): Rat[] {
  const out: Rat[] = [];
  if (iv.lowerClosed) out.push(iv.lower);
  if (iv.upperClosed && !iv.upper.eq(iv.lower)) out.push(iv.upper);
  if (iv.upper.gt(iv.lower)) out.push(iv.lower.add(iv.upper).div(r('2')));
  return out;
}

test('intervalContains respects openness', () => {
  const iv: Interval = { lower: r('0'), lowerClosed: true, upper: r('4'), upperClosed: false };
  assert.ok(intervalContains(iv, r('0')));
  assert.ok(!intervalContains(iv, r('4')));
  assert.ok(intervalContains(iv, r('2')));
});
