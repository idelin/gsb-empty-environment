import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { Pipeline } from '../src/dag.ts';
import type { RoundSpec } from '../src/dag.ts';
import { Rat } from '../src/rational.ts';
import { candidates, searchCounterexample } from '../src/search.ts';
import { U } from '../src/units.ts';

const pricePipe = (name: string, spec: RoundSpec): Pipeline =>
  new Pipeline({
    name,
    nodes: [
      { kind: 'input', id: 'total', source: 'total', unit: U.usd },
      { kind: 'input', id: 'qty', source: 'qty', unit: U.one },
      { kind: 'div', id: 'raw', args: ['total', 'qty'] },
      { kind: 'round', id: 'price', args: ['raw'], spec },
    ],
    output: 'price',
  });

const A = pricePipe('A', { rule: 'half-up', scale: 2 });
const B = pricePipe('B', { rule: 'half-even', scale: 2 });

describe('最小反例搜索', () => {
  it('候选值按复杂度（|n|+d）递增且无重复', () => {
    const cs = candidates([0n, 2n], [1n, 2n]);
    assert.deepEqual(
      cs.map((r) => r.toString()),
      ['0', '1', '2', '1/2'],
    );
  });

  it('找到最小反例并给出精确差异链', () => {
    const res = searchCounterexample(A, B, {
      sources: ['total', 'qty'],
      numeratorRange: [0n, 20n],
      denominators: [1n, 2n, 3n, 4n, 5n, 6n, 7n, 8n],
      budget: 100_000,
    });
    assert.equal(res.status, 'found');
    if (res.status !== 'found') return;
    // 最小反例：total/qty = 1/8 = 0.125（total=1/2, qty=4 与 total=1, qty=8 同复杂度，先枚举到前者）
    const raw = res.inputs.total.div(res.inputs.qty);
    assert.equal(raw.toString(), '1/8');
    assert.equal(res.comparison.finalStatus, 'different');
    assert.equal((res.comparison.finalA.value as Rat).toString(), '13/100');
    assert.equal((res.comparison.finalB.value as Rat).toString(), '3/25');
    assert.equal(res.comparison.origins[0].cause, 'rounding-rule');
    assert.ok(res.comparison.chains[0].reachesOutput);
  });

  it('预算耗尽 → budget-exhausted（未决）', () => {
    const res = searchCounterexample(A, B, {
      sources: ['total', 'qty'],
      numeratorRange: [0n, 1000n],
      denominators: [1n, 2n, 3n, 4n, 5n, 6n, 7n, 8n],
      budget: 5,
    });
    assert.equal(res.status, 'budget-exhausted');
    assert.equal(res.checked, 5);
  });

  it('空间耗尽且无分歧 → exhausted-space', () => {
    const res = searchCounterexample(A, A, {
      sources: ['total', 'qty'],
      numeratorRange: [0n, 2n],
      denominators: [1n, 2n],
      budget: 1000,
    });
    assert.equal(res.status, 'exhausted-space');
  });

  it('mode=any 时中间分歧也算反例', () => {
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
    const a = build('A', 'half-up');
    const b = build('B', 'half-even');
    const res = searchCounterexample(a, b, {
      sources: ['price', 'rate'],
      numeratorRange: [0n, 20n],
      denominators: [1n, 2n, 4n, 5n, 8n, 10n, 100n, 200n, 1000n, 2000n],
      budget: 2_000_000,
      mode: 'any',
    });
    assert.equal(res.status, 'found');
    if (res.status !== 'found') return;
    // 该场景最终输出相同、中间折扣不同
    assert.equal(res.comparison.finalStatus, 'equal');
    assert.ok(res.comparison.intermediateOnly.length > 0);
  });
});
