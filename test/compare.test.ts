import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { comparePipelines } from '../src/compare.ts';
import { applyEdits, explainDivergence } from '../src/counterfactual.ts';
import { Pipeline } from '../src/dag.ts';
import type { RoundSpec } from '../src/dag.ts';
import { Rat } from '../src/rational.ts';
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

describe('DAG 对齐', () => {
  it('等价但结构不同的中间步骤（交换律/结合律）应对齐且判定一致', () => {
    const a = new Pipeline({
      name: 'A',
      nodes: [
        { kind: 'input', id: 'x', source: 'x', unit: U.m },
        { kind: 'input', id: 'y', source: 'y', unit: U.m },
        { kind: 'input', id: 'z', source: 'z', unit: U.m },
        { kind: 'add', id: 't', args: ['x', 'y'] },
        { kind: 'add', id: 'out', args: ['t', 'z'] },
      ],
      output: 'out',
    });
    const b = new Pipeline({
      name: 'B',
      nodes: [
        { kind: 'input', id: 'z', source: 'z', unit: U.m },
        { kind: 'input', id: 'y', source: 'y', unit: U.m },
        { kind: 'input', id: 'x', source: 'x', unit: U.m },
        { kind: 'add', id: 't', args: ['z', 'y'] },
        { kind: 'add', id: 'out', args: ['x', 't'] },
      ],
      output: 'out',
    });
    const cmp = comparePipelines(a, b, { x: Rat.of(1n), y: Rat.of(2n), z: Rat.of(3n) });
    assert.equal(cmp.finalStatus, 'equal');
    assert.equal(cmp.origins.length, 0);
    assert.equal(cmp.intermediateOnly.length, 0);
    // 输出节点应通过结构键对齐（add 被 AC 展平）
    const outPair = cmp.alignment.pairs.find((p) => p.a === 'out');
    assert.ok(outPair);
    assert.equal(outPair.how, 'structural');
  });
});

describe('分歧定位', () => {
  const a = pricePipe('A', { rule: 'half-up', scale: 2 });
  const b = pricePipe('B', { rule: 'half-even', scale: 2 });
  const inputs = { total: Rat.of(1n), qty: Rat.of(8n) };

  it('定位首个舍入分歧原点并给出精确差异链', () => {
    const cmp = comparePipelines(a, b, inputs);
    assert.equal(cmp.finalStatus, 'different');
    assert.equal(cmp.origins.length, 1);
    const o = cmp.origins[0];
    assert.equal(o.cause, 'rounding-rule');
    assert.equal(o.aId, 'price');
    assert.equal(o.aValue.toString(), '13/100');
    assert.equal(o.bValue.toString(), '3/25');
    // 差异链应从原点延伸到输出
    assert.equal(cmp.chains.length, 1);
    assert.ok(cmp.chains[0].reachesOutput);
    // 上游 raw=1/8 两侧一致，不应被误判为分歧
    assert.ok(!cmp.origins.some((x) => x.aId === 'raw'));
  });

  it('反事实替换舍入规则后独立重算，输出与 B 精确一致', () => {
    const cmp = comparePipelines(a, b, inputs);
    const exps = explainDivergence(a, b, inputs, cmp);
    assert.equal(exps.length, 1);
    assert.ok(exps[0].confirmed);
    assert.deepEqual(exps[0].edits[0], { nodeId: 'price', type: 'rounding', spec: { rule: 'half-even', scale: 2 } });
  });

  it('输入一致时两流水精确相等', () => {
    const cmp = comparePipelines(a, b, { total: Rat.of(1n), qty: Rat.of(4n) });
    assert.equal(cmp.finalStatus, 'equal');
    assert.equal(cmp.intermediateOnly.length, 0);
  });
});

describe('单位口径', () => {
  it('漏掉换算 → 单位错误，不被容差掩盖', () => {
    const a = new Pipeline({
      name: 'A',
      nodes: [
        { kind: 'input', id: 'len', source: 'len', unit: U.m },
        { kind: 'convert', id: 'out', args: ['len'], to: U.cm },
      ],
      output: 'out',
    });
    const b = new Pipeline({
      name: 'B',
      nodes: [{ kind: 'input', id: 'len', source: 'len', unit: U.m }],
      output: 'len',
    });
    const cmp = comparePipelines(a, b, { len: Rat.parse('1.5') });
    assert.equal(cmp.finalStatus, 'unit-mismatch');
    assert.equal((cmp.finalA.value as Rat).toString(), '150');
    assert.equal((cmp.finalB.value as Rat).toString(), '3/2');
  });

  it('维度不同 → 硬性单位错误', () => {
    const a = new Pipeline({
      name: 'A',
      nodes: [{ kind: 'input', id: 'v', source: 'v', unit: U.m }],
      output: 'v',
    });
    const b = new Pipeline({
      name: 'B',
      nodes: [{ kind: 'input', id: 'v', source: 'v', unit: U.s }],
      output: 'v',
    });
    const cmp = comparePipelines(a, b, { v: Rat.of(1n) });
    assert.equal(cmp.finalStatus, 'unit-mismatch');
  });
});

describe('最终相同但中间不同', () => {
  it('中间分歧被记录且不误判为完全一致', () => {
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
    const cmp = comparePipelines(a, b, { price: Rat.of(10n), rate: Rat.parse('0.0045') });
    assert.equal(cmp.finalStatus, 'equal');
    assert.equal((cmp.finalA.value as Rat).toString(), '10');
    assert.ok(cmp.intermediateOnly.length >= 1);
    const disc = cmp.intermediateOnly.find((d) => d.aId === 'disc');
    assert.ok(disc);
    assert.equal(disc.cause, 'rounding-rule');
    assert.equal(disc.aValue.toString(), '1/20'); // 0.05
    assert.equal(disc.bValue.toString(), '1/25'); // 0.04
  });
});

describe('分母口径分歧', () => {
  it('const 作为除数 → denominator-convention，反事实替换常量可确认', () => {
    const build = (name: string, denom: bigint): Pipeline =>
      new Pipeline({
        name,
        nodes: [
          { kind: 'input', id: 'x1', source: 'x1', unit: U.usd },
          { kind: 'input', id: 'x2', source: 'x2', unit: U.usd },
          { kind: 'add', id: 'sum', args: ['x1', 'x2'] },
          { kind: 'const', id: 'n', value: Rat.of(denom), unit: U.one },
          { kind: 'div', id: 'avg', args: ['sum', 'n'] },
        ],
        output: 'avg',
      });
    const a = build('A', 2n);
    const b = build('B', 3n);
    const inputs = { x1: Rat.of(1n), x2: Rat.of(2n) };
    const cmp = comparePipelines(a, b, inputs);
    assert.equal(cmp.finalStatus, 'different');
    assert.equal(cmp.origins.length, 1);
    assert.equal(cmp.origins[0].cause, 'denominator-convention');
    assert.equal(cmp.origins[0].aValue.toString(), '2');
    assert.equal(cmp.origins[0].bValue.toString(), '3');
    const exps = explainDivergence(a, b, inputs, cmp);
    assert.ok(exps.some((e) => e.confirmed));
    // 手动验证：A 的分母换成 3 后与 B 一致
    const aPrime = applyEdits(a, [{ nodeId: 'n', type: 'constant', value: Rat.of(3n) }]);
    const fa = aPrime.evaluate(inputs).output;
    assert.equal((fa.value as Rat).toString(), '1');
  });
});

describe('缺少中间证据', () => {
  it('输出不同但中间未记录 → unlocatable', () => {
    const a = pricePipe('A', { rule: 'half-up', scale: 2 });
    const b = new Pipeline({
      name: 'B',
      nodes: [
        { kind: 'input', id: 'total', source: 'total', unit: U.usd },
        { kind: 'input', id: 'qty', source: 'qty', unit: U.one },
        { kind: 'div', id: 'raw', args: ['total', 'qty'], traced: false },
        { kind: 'round', id: 'r', args: ['raw'], spec: { rule: 'half-even', scale: 2 }, traced: false },
        { kind: 'convert', id: 'price', args: ['r'], to: U.usd },
      ],
      output: 'price',
    });
    const cmp = comparePipelines(a, b, { total: Rat.of(1n), qty: Rat.of(8n) });
    assert.equal(cmp.finalStatus, 'unlocatable');
    assert.ok(cmp.missingEvidence.length > 0);
    assert.equal(cmp.origins.length, 0);
  });
});
