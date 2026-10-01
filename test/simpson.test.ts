import { test } from 'node:test';
import assert from 'node:assert/strict';

import { kidneyStoneCase, simpsonAnalysis } from '../src/simpson.ts';
import { add, cmp, equal, rational, ZERO } from '../src/rational.ts';

test('kidney-stone case reverses: A wins both groups, B wins overall', () => {
  const analysis = simpsonAnalysis(kidneyStoneCase());
  assert.equal(analysis.groupWinners.small, 'A');
  assert.equal(analysis.groupWinners.large, 'A');
  assert.equal(analysis.overallWinner, 'B');
  assert.equal(analysis.reversal, true);
});

test('group contributions sum exactly to the overall rate', () => {
  const analysis = simpsonAnalysis(kidneyStoneCase());
  for (const arm of analysis.arms) {
    const total = arm.groups.reduce((acc, g) => add(acc, g.contribution), ZERO);
    assert.ok(
      equal(total, arm.overall),
      `arm ${arm.arm}: contributions ${total.num}/${total.den} != overall ${arm.overall.num}/${arm.overall.den}`,
    );
  }
});

test('weights within an arm sum exactly to 1', () => {
  const analysis = simpsonAnalysis(kidneyStoneCase());
  for (const arm of analysis.arms) {
    const total = arm.groups.reduce((acc, g) => add(acc, g.weight), ZERO);
    assert.ok(equal(total, rational(1n, 1n)));
  }
});

test('exact known rates for the kidney-stone dataset', () => {
  const analysis = simpsonAnalysis(kidneyStoneCase());
  const [a, b] = analysis.arms;
  assert.ok(equal(a.overall, rational(273n, 350n))); // 78.0%
  assert.ok(equal(b.overall, rational(289n, 350n))); // ~82.57%
  assert.ok(equal(a.groups[0].rate, rational(81n, 87n)));
  assert.ok(equal(a.groups[1].rate, rational(192n, 263n)));
  assert.ok(cmp(a.groups[0].rate, b.groups[0].rate) > 0);
  assert.ok(cmp(a.groups[1].rate, b.groups[1].rate) > 0);
  assert.ok(cmp(a.overall, b.overall) < 0);
});

test('narrative explains the reversal via weight x rate mix', () => {
  const analysis = simpsonAnalysis(kidneyStoneCase());
  assert.ok(analysis.narrative.some((line) => line.includes('Simpson reversal')));
  assert.ok(analysis.narrative.some((line) => line.includes('weight')));
});

test('no reversal reported when rankings agree', () => {
  const analysis = simpsonAnalysis([
    {
      arm: 'X',
      groups: [
        { group: 'g1', numerator: 9n, denominator: 10n },
        { group: 'g2', numerator: 8n, denominator: 10n },
      ],
    },
    {
      arm: 'Y',
      groups: [
        { group: 'g1', numerator: 1n, denominator: 10n },
        { group: 'g2', numerator: 2n, denominator: 10n },
      ],
    },
  ]);
  assert.equal(analysis.reversal, false);
  assert.equal(analysis.overallWinner, 'X');
});
