/**
 * 运行样例：
 *  1. 独立线性传播（两个独立砝码求和）
 *  2. 共享误差源（完全相关）：差值抵消校准误差，和值按代数叠加
 *  3. 显式协方差矩阵：合法矩阵传播 + 矛盾矩阵拒绝（负方向证据）
 *  4. 非线性一阶近似（电功率 P = V²/R）
 *
 * 运行：node examples/sample.ts   （Node ≥ 23.6）
 */

import { Decimal } from '../src/decimal.ts';
import { buildCovarianceGraph, independentInputs } from '../src/model.ts';
import {
  NonPSDError,
  propagateFirstOrder,
  propagateLinear,
  propagateLinearWithCovariance,
} from '../src/propagate.ts';
import { formatReport } from '../src/report.ts';

const D = Decimal.from;

// ---------- 场景 1：独立线性传播 ----------
{
  console.log('\n### 场景 1：独立线性传播  y = m1 + m2（两个独立砝码）');
  const { sources, inputs } = independentInputs([
    { id: 'm1', value: D('100.0'), std: D('0.1') },
    { id: 'm2', value: D('50.0'), std: D('0.2') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  const result = propagateLinear(
    [
      { input: 'm1', coeff: D('1') },
      { input: 'm2', coeff: D('1') },
    ],
    graph,
  );
  console.log(formatReport(result));
}

// ---------- 场景 2：共享误差源（完全相关） ----------
{
  console.log('\n### 场景 2：共享校准源  y = x1 - x2（差值）与 y = x1 + x2（和值）');
  const sources = [
    { id: 'cal', std: D('0.05') }, // 共享校准误差（完全相关）
    { id: 'r1', std: D('0.01') },  // x1 独立读数噪声
    { id: 'r2', std: D('0.01') },  // x2 独立读数噪声
  ];
  const inputs = [
    {
      id: 'x1',
      value: D('10.02'),
      contributions: [
        { source: 'cal', coeff: D('1') },
        { source: 'r1', coeff: D('1') },
      ],
    },
    {
      id: 'x2',
      value: D('10.01'),
      contributions: [
        { source: 'cal', coeff: D('1') },
        { source: 'r2', coeff: D('1') },
      ],
    },
  ];
  const graph = buildCovarianceGraph(sources, inputs);

  const diff = propagateLinear(
    [
      { input: 'x1', coeff: D('1') },
      { input: 'x2', coeff: D('-1') },
    ],
    graph,
  );
  console.log('--- 差值：共享校准源完全抵消，Var 中 u(cal) 项系数为 0 ---');
  console.log(formatReport(diff));

  const sum = propagateLinear(
    [
      { input: 'x1', coeff: D('1') },
      { input: 'x2', coeff: D('1') },
    ],
    graph,
  );
  console.log('--- 和值：完全相关误差按代数叠加 (2·u(cal))²，而非独立平方和 2·u(cal)² ---');
  console.log(formatReport(sum));
}

// ---------- 场景 3：显式协方差矩阵 ----------
{
  console.log('\n### 场景 3：显式协方差矩阵');
  const coeffs = [D('1'), D('2')];
  const values = [D('5'), D('7')];
  const cov = [
    [D('0.04'), D('0.01')],
    [D('0.01'), D('0.09')],
  ];
  const ok = propagateLinearWithCovariance(coeffs, values, cov, { labels: ['x1', 'x2'] });
  console.log(formatReport(ok));

  console.log('--- 矛盾协方差矩阵 [[1, 2], [2, 1]]（相关系数 2 > 1）必须被拒绝 ---');
  try {
    propagateLinearWithCovariance([D('1'), D('1')], [D('0'), D('0')], [
      [D('1'), D('2')],
      [D('2'), D('1')],
    ]);
    console.log('错误：未被拒绝！');
  } catch (err) {
    if (err instanceof NonPSDError) {
      console.log(`已拒绝：${err.message}`);
      console.log(`负方向证据 v = [${err.witness.map((d) => d.toString()).join(', ')}]`);
      console.log(`vᵀΣv = ${err.quadraticValue.toString()} < 0`);
    } else {
      throw err;
    }
  }
}

// ---------- 场景 4：非线性一阶近似 ----------
{
  console.log('\n### 场景 4：非线性  P = V²/R（仅一阶近似）');
  const { sources, inputs } = independentInputs([
    { id: 'V', value: D('10'), std: D('0.1') },
    { id: 'R', value: D('100'), std: D('0.5') },
  ]);
  const graph = buildCovarianceGraph(sources, inputs);
  // ∂P/∂V = 2V/R = 0.2，∂P/∂R = -V²/R² = -0.01（标称点处精确值）
  const result = propagateFirstOrder(
    D('1'),
    'P = V^2/R',
    [
      { input: 'V', deriv: D('0.2') },
      { input: 'R', deriv: D('-0.01') },
    ],
    graph,
  );
  console.log(formatReport(result));
}
