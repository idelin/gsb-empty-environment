# exact-quota-distributor

按**有理权重**把整数总量精确分配给一组对象。支持：每项上下限、多个**可重叠**分组上下限、
最大余数法基线、权重更正后的**最少移动再优化**、不可行时**返回缺额并保留未决**，
并为每次求解提供**独立可验证的守恒证书与最优性下界证书**。

全程以 `BigInt` 精确有理数（`Fraction`）计算，**绝不经过 Number 中转大数**。

## 运行环境

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，推荐 23.9+）
- 无需任何第三方依赖

```bash
nvm use            # .nvmrc -> v23.9.0
npm test           # node --test test/*.test.ts（Node 内置测试运行器）
npm run example    # node examples/demo.ts
```

## 核心规则（显式约定）

- **理想份额**：`q_i = total * w_i / W`，精确有理数。
- **最大余数法（基线，仅用于无额外约束）**：先取 `floor(q_i)`，剩余单位按
  精确小数余数从大到小分配；**余数相同按稳定 ID 升序**决定归属。
  总量 1 分三份（权重各 1/3）时，只有 id 最小的一份得 1。
- **有约束求解**：先满足全部整数约束（守恒 `Σx = total`、每项上下限、
  所有重叠分组上下限），再最小化与理想份额的总偏差 `Σ|x_i − q_i|`（精确分数比较）。
  求解是**全局**的精确小规模枚举（边界传播收紧 + 剪枝 + 节点上限），
  不会逐组贪心分配而破坏另一组约束。
- **权重更正（reallocate）**：字典序目标 —— 先最小化总移动量 `Σ|x_i − prev_i|`，
  再最小化总偏差。预算不足时返回 `infeasible` 并把缺额放入 `pending`（保留未决），
  绝不强凑出违反约束的分配。
- **负权重拒绝**：任何负权重、全零权重、负总量都会得到明确的 `error`。
- **不可行**：返回 `{ status: 'infeasible', deficit, pending, violations }`，
  `deficit` 是满足全部下限所需的最小追加预算。
- **舍入规则显式**：`Fraction.toDecimal(places, mode)` 支持
  `down | up | floor | ceil | half-up | half-even`，纯 BigInt 实现；
  内部分配只使用精确的 `floor`/余数比较，无任何隐式舍入。

## API

```ts
import {
  largestRemainder, solve, reallocate, verifyCertificate, Fraction,
} from './src/index.ts';

// 基线：最大余数法（无额外约束时即为偏差最优）
largestRemainder([{ id: 'a', weight: '1/3' }, ...], 1n);

// 一般求解：上下限 + 重叠分组 + 证书
const result = solve({
  total: 10n,
  items: [
    { id: 'a', weight: 1n },
    { id: 'b', weight: 1n },
    { id: 'c', weight: 1n },
  ],
  groups: [
    { id: 'g1', members: ['a', 'b'], lower: 7n },
    { id: 'g2', members: ['b', 'c'], lower: 7n },   // 与 g1 重叠
  ],
});
// -> { status: 'optimal', allocation: { a: 3n, b: 4n, c: 3n }, ... }

// 权重更正：先最少移动，再优化偏差
reallocate({ total: 10n, previous: { a: 5n, b: 5n }, items: [...] });

// 独立验证：重新从原始输入推导并核对守恒、全部约束、目标值与下界
verifyCertificate(input, result); // -> { ok: true, checks: [...] }
```

权重接受 `Fraction | bigint | string`（`"3/7"`、`"1.5"`、`"2"`）；
**Number 输入会被拒绝**以避免精度丢失。

## 证书

- **守恒证书**：`allocatedSum == total`，独立重算。
- **最优性下界证书**：
  - 偏差下界 `LB = max(Σ dist(q_i, ℤ∩[lo_i,hi_i]), 2·max(被迫上调总量, 被迫下调总量))`
    （后者利用守恒 `Σ(x_i − q_i) = 0`）；
  - 移动量下界 `max(Σ dist(prev_i, [lo_i,hi_i]), |total − Σprev|)`；
  - 验证器独立重算并检查 `LB ≤ objective`，且穷举法本身保证全局最优
    （无额外约束时走最大余数快速路径，该路径同样是可证明的偏差最优）。

## 运行样例（`npm run example` 节选）

```
=== 1. Largest remainder baseline: total 1, three equal weights ===
allocation: { a: 1n, b: 0n, c: 0n }     // 只一份得 1，同余数归稳定 id "a"

=== 2. Exact rational weights with a huge total ===
allocation: { x: 142857142857142857142857142858n,
              y: 285714285714285714285714285716n,
              z: 571428571428571428571428571433n }
conserved: true                          // 总量 10^30+7，全程 BigInt 精确

=== 3. Overlapping group bounds, solved globally ===
allocation: { a: 3n, b: 4n, c: 3n }      // g1: a+b>=7 且 g2: b+c>=7 同时满足
objective.deviation: 4/3                 // 精确分数
certificate.conservation: { total: 10, allocatedSum: 10, conserved: true }
certificate.optimality.lowerBound: { deviation: 1 }  // 1 <= 4/3，下界有效
independent verification ok: true        // 9 项独立检查全部 PASS

=== 4. Infeasible lower bounds -> deficit + pending ===
{ status: "infeasible", deficit: 4n, pending: 4n,
  violations: ["item lower bounds need at least 14, budget is 10 (short 4)", ...] }

=== 5. Negative weight rejected ===
{ status: "error", error: 'item "a": negative weight -1/2 rejected' }

=== 6. Re-weighting: movement first, then deviation ===
allocation: { a: 5n, b: 5n }             // 权重 1:9 但零移动优先
objective: { movement: 0n, deviation: 8n }

=== 7. Re-weighting with insufficient budget -> pending ===
{ status: "infeasible", deficit: 2n, pending: 2n, ... }   // 缺额保留未决
```

## 测试结果（`npm test`，Node v23.9.0）

```
ℹ tests 33
ℹ pass 33
ℹ fail 0
```

覆盖：精确分数运算与显式舍入、2^53 以上大数不经 Number、最大余数法
（总量 1 分三份、同余数稳定 ID、10^30 守恒）、上下限与重叠分组全局求解、
不可行缺额/未决、负权拒绝、权重更正的最少移动再优化、
证书的独立验证与篡改检测。

## 目录结构

```
src/fraction.ts          精确有理数（BigInt），显式舍入规则
src/model.ts             输入模型、校验与规范化（负权拒绝等）
src/bounds.ts            边界传播收紧（联合全部约束）与可行性/缺额诊断
src/largestRemainder.ts  最大余数法基线（同余数按稳定 ID）
src/solver.ts            精确求解：无约束快速路径 + 全局枚举（剪枝、节点上限）
src/reweight.ts          权重更正：先最少移动再优化
src/certificates.ts      守恒/最优性下界证书与独立验证器
examples/demo.ts         七个运行样例
test/*.test.ts           Node 内置测试运行器，33 个用例
```
