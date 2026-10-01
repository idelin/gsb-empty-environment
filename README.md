# exact-uncertainty-propagation

基于 BigInt 精确算术的不确定度传播库（TypeScript，零依赖）。

- 输入量值与标准不确定度，支持三种模型：**独立线性**、**显式协方差矩阵**、**共享误差源（协方差图）**
- 线性模型输出**精确方差表达式**与精确有理数方差；非线性模型**仅输出一阶泰勒近似**并显式声明适用范围
- 全部中间计算为 BigInt 精确有理数，**不经过 Number 中转**；唯一舍入点是最终的 `u(y)=√Var(y)` 与显示
- 显式协方差矩阵先作对称性与**精确 LDLᵀ 半正定检验**；非半正定一律拒绝并附**负方向证据**（向量 v 使 vᵀΣv < 0）
- 完全相关误差按代数叠加（不可按独立平方和）；零方差维度不贡献；重复/双计误差源被拒绝

## 运行环境

- Node.js ≥ 23.6（原生支持 TypeScript 类型擦除，直接运行 `.ts`）
- Node 22.6–23.5 需加 `--experimental-strip-types`
- 无第三方依赖

```sh
node --test test/*.test.ts   # 运行测试（Node.js 内置测试运行器）
node examples/sample.ts      # 运行样例
```

## 快速上手

```ts
import { Decimal } from './src/decimal.ts';
import { buildCovarianceGraph, independentInputs } from './src/model.ts';
import { propagateLinear } from './src/propagate.ts';
import { formatReport } from './src/report.ts';

const D = Decimal.from; // 只接受 string | bigint | Decimal，拒绝 number

const { sources, inputs } = independentInputs([
  { id: 'm1', value: D('100.0'), std: D('0.1') },
  { id: 'm2', value: D('50.0'), std: D('0.2') },
]);
const graph = buildCovarianceGraph(sources, inputs);
const result = propagateLinear(
  [{ input: 'm1', coeff: D('1') }, { input: 'm2', coeff: D('1') }],
  graph,
);
console.log(formatReport(result));
// Var(y) = (1)^2·u(m1)^2 + (1)^2·u(m2)^2 = 0.05，u(y) = 0.223607
```

共享误差源（完全相关）：

```ts
const graph = buildCovarianceGraph(
  [{ id: 'cal', std: D('0.05') }],
  [
    { id: 'x1', value: D('10.02'), contributions: [{ source: 'cal', coeff: D('1') }] },
    { id: 'x2', value: D('10.01'), contributions: [{ source: 'cal', coeff: D('1') }] },
  ],
);
// y = x1 + x2 → Var = (2)^2·u(cal)^2（代数叠加，非独立平方和）
// y = x1 - x2 → Var = 0（完全相关误差抵消）
```

显式协方差矩阵（自动半正定检验，矛盾矩阵拒绝并给负方向证据）：

```ts
import { propagateLinearWithCovariance, NonPSDError } from './src/propagate.ts';
propagateLinearWithCovariance([D('1'), D('1')], [D('0'), D('0')],
  [[D('1'), D('2')], [D('2'), D('1')]]);
// 抛出 NonPSDError：负方向证据 v = [-2, 1]，vᵀΣv = -3 < 0
```

非线性（仅一阶）：

```ts
import { propagateFirstOrder } from './src/propagate.ts';
propagateFirstOrder(D('1'), 'P = V^2/R',
  [{ input: 'V', deriv: D('0.2') }, { input: 'R', deriv: D('-0.01') }], graph);
// 结果 kind = 'first-order-approximation'，假设中声明一阶近似适用范围
```

## 舍入规则（显式）

- 加、减、乘、方差二次型、LDL 分解、行列式：**精确**（BigInt 有理数），无舍入。
- 唯一舍入点：`u(y) = √Var(y)` 与除法/开方式显示，按 `RoundingSpec` 舍入，
  默认 `{ sigDigits: 6, mode: 'half-even' }`（恰为一半向偶数）。
  可选模式：`half-even | half-up | down | up | floor | ceiling`。
- 舍入判定基于精确余数比较（含 sticky 传递），不存在双重舍入误差。
- `Decimal.toSigString(n)` 以恰好 n 位有效数字显示（右侧补零），仅作显示。

## 校验与正确性论证方式

- 测试全部为**精确代数恒等式**与**参考矩阵交叉验证**：
  - 行列式：高斯消元（`src/matrix.ts`）对 Laplace 展开（测试内独立实现）；
  - 半正定：精确 LDLᵀ 对 Sylvester 全部主子式判据（测试内独立实现）；
  - 负方向证据：返回前用精确二次型复核 `vᵀΣv < 0`；
  - 协方差图与显式矩阵两条独立路径对同一模型给出逐位一致的结果。
- **不以蒙特卡洛经验样本波动作为传播正确性的证明**；抽样演示不属于本库的验证手段。

## 限制

- 非线性模型只给一阶泰勒近似；偏导数由调用方在标称点处提供，本库不做数值微分。
- `Decimal.from` 拒绝 `number`（防止大数经 Number 中转丢失精度），请用字符串或 bigint。
- 加减法对齐时数量级差异超过 10^200000 会抛错（防止内存耗尽）。

## 目录结构

```
src/decimal.ts     精确十进制（BigInt），显式舍入规则
src/rational.ts    精确有理数，正确舍入的除法/开方
src/matrix.ts      精确矩阵代数：二次型、行列式、LDLᵀ 半正定检验（含负方向证据）
src/model.ts       误差源协方差图：共享源、双计保护、未使用源警告
src/propagate.ts   传播入口：线性（图/矩阵）、非线性一阶
src/report.ts      中文报告：假设、精确系数、精确方差表达式、舍入说明
examples/sample.ts 四个场景的运行样例
test/*.test.ts     Node.js 内置测试（node:test）
```
