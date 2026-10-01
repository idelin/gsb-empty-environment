# exact-uncertainty-propagation

基于 BigInt 精确有理数的不确定度传播库（TypeScript，零依赖）。

## 设计规则

- **精确算术**：所有量值以 `Rational`（BigInt 分子/分母）表示；十进制字符串直接解析为 BigInt，不经过 Number 中转，超过 2^53 的大数保持精确（见 `test/rational.test.ts` 中 `9007199254740993` 与 `10^30 + 1` 用例）。
- **显式舍入**：舍入只发生在两处——方差开方求标准不确定度、十进制格式化——均需显式 `RoundingMode`（`'half-even'`（默认）/ `'half-up'` / `'down'` / `'up'`），并在报告中注明舍入规则与位数；方差、协方差本身始终是精确有理数。
- **线性精确**：线性模型 `y = A x + b` 的输出协方差 `A Σ Aᵀ` 精确计算；报告回显精确线性系数，并给出符号化的精确方差表达（如 `Var(y) = (2)^2*Var(x1) + (3)^2*Var(x2) = 2/5`）。
- **非线性仅一阶**：非线性模型只按工作点处偏导数做一阶泰勒传播，且必须显式 `firstOrderDeclared: true`，否则拒绝计算；报告中声明近似范围（`validityNote`）。
- **完全相关不按独立平方和**：共享底层误差源的输入通过源模型 `Σ = G D Gᵀ` 构造协方差，完全相关误差线性相加而非根方和（`u(x1+x2) = u1 + u2`）。
- **零方差维不贡献**：零方差源/维在方差表达式与数值中均不出现。
- **非半正定拒绝**：直接给定的协方差矩阵先做精确对称性与半正定检验（LDLᵀ，对称选主元，全有理数）；矛盾协方差（如 |ρ| > 1）抛出 `NonPSDError`，携带负方向证据：精确向量 `v` 使 `vᵀΣv < 0`。
- **重复来源防双计**：同一输入重复引用同一源时增益合并（计一次）并告警；方差与增益签名完全相同的两个源被标记为疑似同一物理误差源（避免低估协方差）。
- **不以经验样本波动作证**：测试全部对照手工推导的精确代数（小矩阵参考验证），不使用 Monte Carlo 采样作为传播正确性证明。

## 运行

需要 Node.js ≥ 22.6（使用内置类型擦除运行 TypeScript；Node ≥ 23.6 可去掉 flag）：

```sh
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

如需 `tsc` 类型检查，联网后 `npm i -D typescript @types/node && npx tsc --noEmit`（`tsconfig.json` 已配置）。

## API 概览

```ts
import { propagateLinear, propagateFirstOrder, buildSourceModel } from './src/index.ts';

// 独立/给定协方差的线性传播
propagateLinear({
  inputs: [{ id: 'x1', value: '10' }, { id: 'x2', value: '20' }],
  outputs: [{ id: 'y', coefficients: [2, 3] }],
  covariance: [['0.01', 0], [0, '0.04']],
});

// 共享底层误差源（构造协方差图，自动防双计）
propagateLinear({
  inputs: [
    { id: 'x1', value: '10.0', contributions: [{ source: 'cal' }, { source: 'r1' }] },
    { id: 'x2', value: '20.0', contributions: [{ source: 'cal' }, { source: 'r2' }] },
  ],
  outputs: [{ id: 'sum', coefficients: [1, 1] }],
  sources: [{ id: 'cal', std: '0.3' }, { id: 'r1', std: '0.1' }, { id: 'r2', std: '0.1' }],
});

// 非线性：必须声明一阶近似
propagateFirstOrder({
  inputs: [{ id: 'V', value: '2' }, { id: 'I', value: '5' }],
  outputs: [{ id: 'P', valueAtPoint: '10', derivatives: ['5', '2'] }],
  covariance: [['0.01', 0], [0, '0.04']],
  firstOrderDeclared: true,
  validityNote: '工作点 (2, 5) 附近小信号范围',
});
```

## 目录

- `src/rational.ts` — BigInt 精确有理数、十进制解析、显式舍入、精确开方
- `src/matrix.ts` — 有理数矩阵代数、精确半正定检验（含负方向证据向量）
- `src/sources.ts` — 误差源 → 协方差图（`Σ = G D Gᵀ`）、重复来源检测
- `src/propagate.ts` — 线性精确传播 / 声明式一阶非线性传播、报告假设与精确系数
- `test/` — node:test 内置测试（29 个用例，全部为精确代数参考验证）
- `examples/demo.ts` — 四个运行样例（独立线性、共享源完全相关、一阶非线性、矛盾协方差拒绝）
