import test from 'node:test';
import assert from 'node:assert/strict';
import { toElements } from '../src/elements.ts';
import { layoutOptimal } from '../src/dp.ts';
import { layoutGreedy } from '../src/greedy.ts';
import { layoutExhaustive } from '../src/exhaustive.ts';
import { GLYPHS, GREEDY_TRAP, HYPHEN, SHORT, TIE } from '../src/fixtures.ts';

const fixtures = [SHORT, GREEDY_TRAP, TIE, HYPHEN];

for (const fixture of fixtures) {
  test(`DP 与穷举对照一致: ${fixture.name}`, () => {
    const elements = toElements(fixture.paragraph, GLYPHS.hyphenWidth);
    const dp = layoutOptimal(elements, fixture.config);
    const exhaustive = layoutExhaustive(elements, fixture.config);
    assert.equal(exhaustive.status, 'ok');
    assert.ok(Math.abs((exhaustive.cost as number) - dp.cost) <= 1e-9,
      `DP=${dp.cost} 穷举=${exhaustive.cost}`);
  });
}

test('贪心非最优样例: DP 选择全局更低代价', () => {
  const elements = toElements(GREEDY_TRAP.paragraph, GLYPHS.hyphenWidth);
  const greedy = layoutGreedy(elements, GREEDY_TRAP.config);
  const dp = layoutOptimal(elements, GREEDY_TRAP.config);
  assert.ok(dp.cost < greedy.cost - 1e-9, `DP=${dp.cost} 应严格小于贪心=${greedy.cost}`);
  assert.equal(dp.breaks.length, 1); // 首行只放 "wide"
  assert.equal(dp.lines[0].measure.text, 'wide');
});

test('相同代价按较早断点决胜', () => {
  const elements = toElements(TIE.paragraph, GLYPHS.hyphenWidth);
  const dp = layoutOptimal(elements, TIE.config);
  const exhaustive = layoutExhaustive(elements, TIE.config);
  assert.ok(Math.abs(dp.cost - 0.16) <= 1e-9, `成本应为 0.16, 实际 ${dp.cost}`);
  assert.deepEqual(dp.breaks, [1]); // [80][12,30] 而非 [80,12][30]
  assert.deepEqual(exhaustive.breaks, [1]);
});
