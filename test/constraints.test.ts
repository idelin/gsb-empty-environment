import test from 'node:test';
import assert from 'node:assert/strict';
import { toElements } from '../src/elements.ts';
import { layoutOptimal } from '../src/dp.ts';
import { layoutGreedy } from '../src/greedy.ts';
import { layoutExhaustive } from '../src/exhaustive.ts';
import { formatLayout } from '../src/report.ts';
import { ADJACENCY, GLYPHS, HYPHEN, LONG_WORD } from '../src/fixtures.ts';

test('断词预算: 跨行一致的断词次数上限', () => {
  const elements = toElements(HYPHEN.paragraph, GLYPHS.hyphenWidth);
  const budget0 = layoutOptimal(elements, { ...HYPHEN.config, hyphenBudget: 0 });
  const budget2 = layoutOptimal(elements, { ...HYPHEN.config, hyphenBudget: 2 });
  const budget3 = layoutOptimal(elements, { ...HYPHEN.config, hyphenBudget: 3 });
  assert.equal(budget0.hyphenCount, 0);
  assert.ok(budget2.hyphenCount <= 2);
  assert.ok(budget3.hyphenCount <= 3);
  assert.ok(budget3.cost <= budget2.cost + 1e-9);
  assert.ok(budget2.cost <= budget0.cost + 1e-9);
  assert.ok(budget3.hyphenCount >= 1, '断词应确实被使用');
});

test('禁止连续断词: 任意相邻两行不得都以断词结尾', () => {
  const elements = toElements(HYPHEN.paragraph, GLYPHS.hyphenWidth);
  const result = layoutOptimal(elements, HYPHEN.config);
  for (let i = 1; i < result.lines.length; i++) {
    assert.ok(
      !(result.lines[i - 1].measure.hyphenated && result.lines[i].measure.hyphenated),
      `行 ${i} 与行 ${i + 1} 连续断词`,
    );
  }
});

test('相邻行宽度差罚分: 权重增大则相邻剩余宽差异不增', () => {
  const elements = toElements(ADJACENCY.paragraph, GLYPHS.hyphenWidth);
  const adjSum = (adjacencyWeight: number): number => {
    const result = layoutOptimal(elements, { ...ADJACENCY.config, adjacencyWeight });
    let sum = 0;
    for (let i = 1; i < result.lines.length; i++) {
      sum += Math.abs(result.lines[i - 1].measure.remaining - result.lines[i].measure.remaining);
    }
    return sum;
  };
  const loose = adjSum(0);
  const tight = adjSum(50);
  assert.ok(tight < loose, `权重 50 的邻行差 ${tight} 应小于权重 0 的 ${loose}`);
});

test('不可断长词: 允许溢出、单独报告、不死循环', () => {
  const elements = toElements(LONG_WORD.paragraph, GLYPHS.hyphenWidth);
  const dp = layoutOptimal(elements, LONG_WORD.config);
  const overflowLines = dp.lines.filter((l) => l.measure.overflow > 0);
  assert.equal(overflowLines.length, 1);
  assert.equal(overflowLines[0].measure.text, 'superlongunbreakableword');
  const report = formatLayout(dp, '长词', true);
  assert.match(report, /不可断长词溢出: 行2/);
  const greedy = layoutGreedy(elements, LONG_WORD.config); // 也应终止
  assert.ok(greedy.lines.some((l) => l.measure.overflow > 0));
});

test('负剩余宽与不可断长词分开报告', () => {
  const elements = toElements(LONG_WORD.paragraph, GLYPHS.hyphenWidth);
  const report = formatLayout(layoutOptimal(elements, LONG_WORD.config), '长词', true);
  assert.match(report, /\[报告\] 负剩余宽行:/);
  assert.match(report, /\[报告\] 不可断长词溢出:/);
});

test('预算中断单独报告, 不声称最优', () => {
  const elements = toElements(HYPHEN.paragraph, GLYPHS.hyphenWidth);
  const result = layoutExhaustive(elements, HYPHEN.config, 3);
  assert.equal(result.status, 'budget-exceeded');
  assert.equal(result.cost, null);
  assert.equal(result.breaks, null);
});
