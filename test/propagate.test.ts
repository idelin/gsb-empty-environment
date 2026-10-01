import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { Rational } from '../src/rational.ts';
import { quadraticForm, matFromDecimals } from '../src/matrix.ts';
import { buildCovarianceGraph, independentInputs, ModelError } from '../src/model.ts';
import {
  NonPSDError,
  propagateFirstOrder,
  propagateLinear,
  propagateLinearWithCovariance,
} from '../src/propagate.ts';
import { formatReport } from '../src/report.ts';

const D = Decimal.from;

test('独立线性传播：Var 精确、系数与表达式正确', () => {
  const { sources, inputs } = independentInputs([
    { id: 'x1', value: D('1'), std: D('0.1') },
    { id: 'x2', value: D('2'), std: D('0.2') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  const r = propagateLinear(
    [
      { input: 'x1', coeff: D('3') },
      { input: 'x2', coeff: D('4') },
    ],
    graph,
  );
  assert.equal(r.kind, 'linear-exact');
  assert.equal(r.value.toString(), '11'); // 3·1 + 4·2
  assert.equal(r.variance.toString(), '73/100'); // 9·0.01 + 16·0.04
  assert.equal(r.varianceExpr, '(3)^2·u(x1)^2 + (4)^2·u(x2)^2');
  assert.equal(r.std.toSigString(6), '0.854400');
  assert.deepEqual(
    r.coefficients.map((c) => [c.input, c.coeff.toString()]),
    [
      ['x1', '3'],
      ['x2', '4'],
    ],
  );
});

test('完全相关误差：按代数叠加，不可按独立平方和', () => {
  const sources = [{ id: 'cal', std: D('0.1') }];
  const inputs = [
    { id: 'x1', value: D('0'), contributions: [{ source: 'cal', coeff: D('1') }] },
    { id: 'x2', value: D('0'), contributions: [{ source: 'cal', coeff: D('1') }] },
  ];
  const graph = buildCovarianceGraph(sources, inputs);

  const sum = propagateLinear(
    [
      { input: 'x1', coeff: D('1') },
      { input: 'x2', coeff: D('1') },
    ],
    graph,
  );
  // (1+1)^2·u^2 = 4·0.01 = 0.04，而独立平方和会错误地给出 0.02
  assert.equal(sum.variance.toDecimalExact()!.toString(), '0.04');
  assert.notEqual(sum.variance.toDecimalExact()!.toString(), '0.02');
  assert.equal(sum.std.toString(), '0.2');
  assert.equal(sum.varianceExpr, '(2)^2·u(cal)^2');

  const diff = propagateLinear(
    [
      { input: 'x1', coeff: D('1') },
      { input: 'x2', coeff: D('-1') },
    ],
    graph,
  );
  // 差值：完全相关误差完全抵消
  assert.equal(diff.variance.toString(), '0');
  assert.equal(diff.std.toString(), '0');
  assert.equal(diff.varianceExpr, '0');
});

test('共享源 + 独立源混合：协方差图与显式矩阵两条路径结果一致', () => {
  const sources = [
    { id: 'cal', std: D('0.05') },
    { id: 'r1', std: D('0.01') },
    { id: 'r2', std: D('0.01') },
  ];
  const inputs = [
    {
      id: 'x1',
      value: D('0'),
      contributions: [
        { source: 'cal', coeff: D('1') },
        { source: 'r1', coeff: D('1') },
      ],
    },
    {
      id: 'x2',
      value: D('0'),
      contributions: [
        { source: 'cal', coeff: D('1') },
        { source: 'r2', coeff: D('1') },
      ],
    },
  ];
  const graph = buildCovarianceGraph(sources, inputs);
  const viaGraph = propagateLinear(
    [
      { input: 'x1', coeff: D('1') },
      { input: 'x2', coeff: D('2') },
    ],
    graph,
  );
  // 参照：Cov = [[0.0026, 0.0025], [0.0025, 0.0026]]，Var = 0.0026 + 4·0.0026 + 4·0.0025
  const viaMatrix = propagateLinearWithCovariance(
    [D('1'), D('2')],
    [D('0'), D('0')],
    [
      [D('0.0026'), D('0.0025')],
      [D('0.0025'), D('0.0026')],
    ],
  );
  assert.equal(viaGraph.variance.toString(), viaMatrix.variance.toString());
  assert.equal(viaGraph.variance.toString(), '23/1000'); // 0.0026 + 4·0.0026 + 4·0.0025
  assert.equal(viaGraph.variance.toDecimalExact()!.toString(), '0.023');
});

test('零方差维度不贡献', () => {
  const sources = [{ id: 's1', std: D('0.1') }];
  const inputs = [
    { id: 'x1', value: D('1'), contributions: [{ source: 's1', coeff: D('1') }] },
    { id: 'x2', value: D('5'), contributions: [] }, // 零方差
  ];
  const graph = buildCovarianceGraph(sources, inputs);
  const withZero = propagateLinear(
    [
      { input: 'x1', coeff: D('3') },
      { input: 'x2', coeff: D('7') },
    ],
    graph,
  );
  const without = propagateLinear([{ input: 'x1', coeff: D('3') }], graph);
  assert.equal(withZero.variance.toString(), without.variance.toString());
  assert.deepEqual(withZero.nonContributing, [{ input: 'x2', reason: '零方差维度，不贡献' }]);
});

test('双计保护：重复源、输入重复引用同一源、未知源均被拒绝', () => {
  assert.throws(
    () =>
      buildCovarianceGraph(
        [
          { id: 's', std: D('0.1') },
          { id: 's', std: D('0.2') },
        ],
        [],
      ),
    ModelError,
  );
  assert.throws(
    () =>
      buildCovarianceGraph(
        [{ id: 's', std: D('0.1') }],
        [
          {
            id: 'x',
            value: D('0'),
            contributions: [
              { source: 's', coeff: D('1') },
              { source: 's', coeff: D('1') },
            ],
          },
        ],
      ),
    /双计/,
  );
  assert.throws(
    () =>
      buildCovarianceGraph(
        [],
        [{ id: 'x', value: D('0'), contributions: [{ source: 'ghost', coeff: D('1') }] }],
      ),
    /未声明的误差源/,
  );
});

test('未使用的源产生警告而非错误', () => {
  const graph = buildCovarianceGraph(
    [
      { id: 'used', std: D('0.1') },
      { id: 'unused', std: D('0.5') },
    ],
    [{ id: 'x', value: D('0'), contributions: [{ source: 'used', coeff: D('1') }] }],
  );
  assert.equal(graph.warnings.length, 1);
  assert.match(graph.warnings[0]!, /unused/);
});

test('显式协方差：合法相关矩阵精确传播', () => {
  const r = propagateLinearWithCovariance(
    [D('1'), D('2')],
    [D('5'), D('7')],
    [
      [D('0.04'), D('0.01')],
      [D('0.01'), D('0.09')],
    ],
  );
  assert.equal(r.value.toString(), '19');
  assert.equal(r.variance.toString(), '11/25'); // 0.04 + 4·0.09 + 2·1·2·0.01 = 0.44
  assert.equal(r.std.toSigString(6), '0.663325');
  assert.match(r.varianceExpr, /Cov\(x1,x2\)/);
});

test('显式协方差：秩 1 完全相关矩阵通过半正定检验', () => {
  const r = propagateLinearWithCovariance(
    [D('1'), D('1')],
    [D('0'), D('0')],
    [
      [D('0.01'), D('0.01')],
      [D('0.01'), D('0.01')],
    ],
  );
  assert.equal(r.variance.toDecimalExact()!.toString(), '0.04'); // 非独立平方和 0.02
});

test('矛盾协方差矩阵：拒绝并给出负方向证据', () => {
  let caught: NonPSDError | null = null;
  try {
    propagateLinearWithCovariance([D('1'), D('1')], [D('0'), D('0')], [
      [D('1'), D('2')],
      [D('2'), D('1')],
    ]);
  } catch (err) {
    caught = err as NonPSDError;
  }
  assert.ok(caught instanceof NonPSDError);
  assert.match(caught.message, /负方向证据/);
  assert.equal(caught.quadraticValue.toString(), '-3');
  // 证据向量必须精确满足 vᵀΣv < 0
  const m = matFromDecimals([
    [D('1'), D('2')],
    [D('2'), D('1')],
  ]);
  const check = quadraticForm(caught.witnessExact, m);
  assert.equal(check.toString(), caught.quadraticValue.toString());
  assert.ok(check.sign < 0);
});

test('非对称协方差矩阵被拒绝', () => {
  assert.throws(
    () =>
      propagateLinearWithCovariance([D('1'), D('1')], [D('0'), D('0')], [
        [D('1'), D('0.5')],
        [D('0.2'), D('1')],
      ]),
    /不对称/,
  );
});

test('非线性：仅一阶近似并显式声明', () => {
  const { sources, inputs } = independentInputs([
    { id: 'V', value: D('10'), std: D('0.1') },
    { id: 'R', value: D('100'), std: D('0.5') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  const r = propagateFirstOrder(
    D('1'),
    'P = V^2/R',
    [
      { input: 'V', deriv: D('0.2') },
      { input: 'R', deriv: D('-0.01') },
    ],
    graph,
  );
  assert.equal(r.kind, 'first-order-approximation');
  assert.equal(r.variance.toString(), '17/40000'); // 0.04·0.01 + 0.0001·0.25
  assert.equal(r.variance.toDecimalExact()!.toString(), '0.000425');
  assert.equal(r.std.toSigString(6), '0.0206155');
  assert.ok(r.assumptions.some((a) => a.includes('一阶泰勒近似')));
});

test('大数不经 Number 中转：超出 float64 的尾数保持精确', () => {
  // Number('10000000000000000001') === 1e19，浮点实现会把方差算成 0.09
  const { sources, inputs } = independentInputs([
    { id: 'x', value: D('0'), std: D('0.00000000000000000003') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  const r = propagateLinear([{ input: 'x', coeff: D('10000000000000000001') }], graph);
  // (1e19+1)^2 · 9e-40 = 0.09 + 1.8e-20 + 9e-40，逐位精确
  const expected = '0.09' + '0'.repeat(17) + '18' + '0'.repeat(18) + '9';
  assert.equal(r.variance.toDecimalExact()!.toString(), expected);
});

test('报告包含假设、精确系数、方差表达式与舍入规则', () => {
  const { sources, inputs } = independentInputs([
    { id: 'm1', value: D('100.0'), std: D('0.1') },
    { id: 'm2', value: D('50.0'), std: D('0.2') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  const r = propagateLinear(
    [
      { input: 'm1', coeff: D('1') },
      { input: 'm2', coeff: D('1') },
    ],
    graph,
  );
  const report = formatReport(r);
  assert.match(report, /\[假设\]/);
  assert.match(report, /\[精确线性系数\]/);
  assert.match(report, /Var\(y\) = \(1\)\^2·u\(m1\)\^2 \+ \(1\)\^2·u\(m2\)\^2/);
  assert.match(report, /= 0\.05/);
  assert.match(report, /u\(y\) = 0\.223607/);
  assert.match(report, /half-even/);
  assert.match(report, /BigInt 精确有理数/);
});
