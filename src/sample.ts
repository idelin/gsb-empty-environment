/**
 * 运行样例：5 个场景覆盖全部核心能力。
 * 运行：node src/sample.ts   （需要 Node >= 22.6，推荐 23+）
 */

import { comparePipelines } from './compare.ts';
import { explainDivergence } from './counterfactual.ts';
import { Pipeline } from './dag.ts';
import type { RoundSpec } from './dag.ts';
import { Rat } from './rational.ts';
import { renderComparison, renderExplanations, renderSearch } from './report.ts';
import { searchCounterexample } from './search.ts';
import { U } from './units.ts';

const show = (title: string, body: string): void => {
  console.log(`\n${'='.repeat(72)}\n【${title}】\n${'-'.repeat(72)}\n${body}`);
};

/** 场景 1：单价 = 总额/数量，A 用 half-up、B 用 half-even 保留 2 位小数。 */
function unitPricePipeline(name: string, spec: RoundSpec): Pipeline {
  return new Pipeline({
    name,
    nodes: [
      { kind: 'input', id: 'total', source: 'total', unit: U.usd },
      { kind: 'input', id: 'qty', source: 'qty', unit: U.one },
      { kind: 'div', id: 'raw', args: ['total', 'qty'] },
      { kind: 'round', id: 'price', args: ['raw'], spec },
    ],
    output: 'price',
  });
}

function scenario1(): void {
  const a = unitPricePipeline('A(half-up)', { rule: 'half-up', scale: 2 });
  const b = unitPricePipeline('B(half-even)', { rule: 'half-even', scale: 2 });
  const inputs = { total: Rat.of(1n), qty: Rat.of(8n) };
  const cmp = comparePipelines(a, b, inputs);
  show('场景 1：舍入规则分歧（1/8=0.125 保留 2 位小数）', renderComparison(cmp));
  console.log(renderExplanations(explainDivergence(a, b, inputs, cmp)));

  const found = searchCounterexample(a, b, {
    sources: ['total', 'qty'],
    numeratorRange: [0n, 20n],
    denominators: [1n, 2n, 3n, 4n, 5n, 6n, 7n, 8n],
    budget: 100_000,
  });
  show('场景 1b：自动搜索最小反例', renderSearch(found));

  const pending = searchCounterexample(a, b, {
    sources: ['total', 'qty'],
    numeratorRange: [0n, 1000n],
    denominators: [1n, 2n, 3n, 4n, 5n, 6n, 7n, 8n],
    budget: 5,
  });
  show('场景 1c：预算不足时的未决状态', renderSearch(pending));
}

/** 场景 2：B 漏掉了 m→cm 换算，单位口径错误（不容差掩盖）。 */
function scenario2(): void {
  const a = new Pipeline({
    name: 'A(显式换算)',
    nodes: [
      { kind: 'input', id: 'len', source: 'len', unit: U.m },
      { kind: 'round', id: 'r', args: ['len'], spec: { rule: 'half-up', scale: 2 } },
      { kind: 'convert', id: 'out', args: ['r'], to: U.cm },
    ],
    output: 'out',
  });
  const b = new Pipeline({
    name: 'B(漏换算)',
    nodes: [
      { kind: 'input', id: 'len', source: 'len', unit: U.m },
      { kind: 'round', id: 'r', args: ['len'], spec: { rule: 'half-up', scale: 2 } },
    ],
    output: 'r',
  });
  const inputs = { len: Rat.parse('1.005') };
  const cmp = comparePipelines(a, b, inputs);
  show('场景 2：单位口径错误（A 输出 cm，B 漏掉 m→cm 换算）', renderComparison(cmp));
}

/** 场景 3：最终相同但中间不同（折扣舍入被后续取整吸收）。 */
function scenario3(): void {
  const build = (name: string, rule: RoundSpec['rule']): Pipeline =>
    new Pipeline({
      name,
      nodes: [
        { kind: 'input', id: 'price', source: 'price', unit: U.usd },
        { kind: 'input', id: 'rate', source: 'rate', unit: U.one },
        { kind: 'mul', id: 'rawDisc', args: ['price', 'rate'] },
        { kind: 'round', id: 'disc', args: ['rawDisc'], spec: { rule, scale: 2 } },
        { kind: 'sub', id: 'net', args: ['price', 'disc'] },
        { kind: 'round', id: 'out', args: ['net'], spec: { rule: 'half-up', scale: 0 } },
      ],
      output: 'out',
    });
  const a = build('A(折扣half-up)', 'half-up');
  const b = build('B(折扣half-even)', 'half-even');
  const inputs = { price: Rat.of(10n), rate: Rat.parse('0.0045') };
  const cmp = comparePipelines(a, b, inputs);
  show('场景 3：最终相同但中间不同（0.045 折扣 → 0.05 vs 0.04，取整后同为 10）', renderComparison(cmp));
}

/** 场景 4：分母口径分歧（总体均值 /n vs 样本均值 /(n-1)）。 */
function scenario4(): void {
  const build = (name: string, denom: bigint): Pipeline =>
    new Pipeline({
      name,
      nodes: [
        { kind: 'input', id: 'x1', source: 'x1', unit: U.usd },
        { kind: 'input', id: 'x2', source: 'x2', unit: U.usd },
        { kind: 'input', id: 'x3', source: 'x3', unit: U.usd },
        { kind: 'add', id: 's12', args: ['x1', 'x2'] },
        { kind: 'add', id: 'sum', args: ['s12', 'x3'] },
        { kind: 'const', id: 'n', value: Rat.of(denom), unit: U.one },
        { kind: 'div', id: 'avg', args: ['sum', 'n'] },
      ],
      output: 'avg',
    });
  const a = build('A(除以n=3)', 3n);
  const b = build('B(除以n-1=2)', 2n);
  const inputs = { x1: Rat.of(1n), x2: Rat.of(1n), x3: Rat.of(1n) };
  const cmp = comparePipelines(a, b, inputs);
  show('场景 4：分母口径分歧（sum/3 vs sum/2）', renderComparison(cmp));
  console.log(renderExplanations(explainDivergence(a, b, inputs, cmp)));
}

/** 场景 5：B 未记录中间值，输出不同但无法定位。 */
function scenario5(): void {
  const a = unitPricePipeline('A(half-up)', { rule: 'half-up', scale: 2 });
  const b = new Pipeline({
    name: 'B(中间未记录)',
    nodes: [
      { kind: 'input', id: 'total', source: 'total', unit: U.usd },
      { kind: 'input', id: 'qty', source: 'qty', unit: U.one },
      { kind: 'div', id: 'raw', args: ['total', 'qty'], traced: false },
      { kind: 'round', id: 'r', args: ['raw'], spec: { rule: 'half-even', scale: 2 }, traced: false },
      { kind: 'convert', id: 'price', args: ['r'], to: U.usd },
    ],
    output: 'price',
  });
  const inputs = { total: Rat.of(1n), qty: Rat.of(8n) };
  const cmp = comparePipelines(a, b, inputs);
  show('场景 5：缺少中间证据 → 标记无法定位（而非猜测）', renderComparison(cmp));
}

scenario1();
scenario2();
scenario3();
scenario4();
scenario5();
