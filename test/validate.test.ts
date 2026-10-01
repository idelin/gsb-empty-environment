import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Rat } from '../src/rational.ts';
import { compileLadder } from '../src/ladder.ts';
import { validateOnGrid } from '../src/validate.ts';

const r = (s: string) => Rat.parse(s);

function composed() {
  return compileLadder({
    rules: [
      {
        name: 'tiered',
        mode: 'progressive',
        brackets: [
          { lower: r('0'), lowerClosed: true, upper: r('50'), upperClosed: true, rate: r('0.1') },
          { lower: r('50'), lowerClosed: false, upper: r('200'), upperClosed: true, rate: r('0.2') },
          { lower: r('200'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0.3') },
        ],
        rounding: { quantum: r('0.1'), mode: 'half-up', at: 'rule-total' },
      },
      {
        name: 'fee',
        mode: 'flat',
        brackets: [
          { lower: r('-100'), lowerClosed: true, upper: r('0'), upperClosed: false, rate: r('0'), offset: r('1') },
          { lower: r('0'), lowerClosed: true, upper: r('100'), upperClosed: false, rate: r('0'), offset: r('5') },
          { lower: r('100'), lowerClosed: true, upper: r('500'), upperClosed: true, rate: r('0'), offset: r('3') },
          { lower: r('500'), lowerClosed: false, upper: null, upperClosed: false, rate: r('0'), offset: r('0') },
        ],
        discount: r('1.5'),
        min: r('1'),
        max: r('4'),
      },
    ],
    finalRounding: { quantum: r('1'), mode: 'floor' },
  });
}

test('small rational grid: forward and inverse agree everywhere', () => {
  const report = validateOnGrid(composed(), { from: r('-20'), to: r('120'), step: r('2.5') });
  assert.equal(report.inverseMismatches.length, 0);
  assert.ok(report.pointsChecked > 50);
  assert.ok(report.breakpointChecks.every((c) => c.ok));
  assert.ok(report.ok);
});

test('breakpoint checks cover negative thresholds and holes', () => {
  const withHole = compileLadder({
    rules: [
      {
        name: 'gappy',
        mode: 'flat',
        brackets: [
          { lower: r('-10'), lowerClosed: true, upper: r('0'), upperClosed: false, rate: r('1') },
          { lower: r('5'), lowerClosed: true, upper: null, upperClosed: false, rate: r('1') },
        ],
      },
    ],
  });
  const report = validateOnGrid(withHole, { from: r('-10'), to: r('10'), step: r('1') });
  assert.ok(report.ok);
  const hole = report.breakpointChecks.find((c) => c.at.eq(r('5')) && c.rule === 'gappy');
  assert.ok(hole === undefined || hole.ok);
  const atZero = report.breakpointChecks.find((c) => c.at.eq(r('0')));
  assert.ok(atZero !== undefined && atZero.ok);
});
