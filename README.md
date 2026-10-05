# 拆单求解器（order-splitting-solver）

为订单选择发货仓并分配各仓发货数量。TypeScript 实现，无第三方依赖。

## 问题模型

- 每个仓有**启用运费** `baseFee`（从该仓发货即收一次）、**单件运费** `perItemFee`、按商品的**库存** `stock`。
- 可选**免运门槛** `freeShippingThreshold`：从该仓发出的总件数达到门槛时，该仓运费全免。
  因此"多取一件"（超出订单需求购买）可能反而更便宜——求解器允许超量分配（不超过库存），
  并通过 `extraUnits` 报告多买的件数。
- 同种商品可拆分到多个仓。
- 规模上限：8 个仓、12 种商品（`MAX_WAREHOUSES` / `MAX_PRODUCTS`）。

## 优化目标（字典序）

1. **总运费最低**；
2. **包裹数最少**（实际发货的仓库数）；
3. 超需求件数最少（仅作并列时的收尾裁决）。

## 算法

- 枚举每个仓的状态（不用 / 付费模式 / 免运模式），最多 `3^8 = 6561` 种组合。
- 每种组合化为**带下界的最小费用循环流**（运输问题，全幺模矩阵保证整数解）：
  - `S -> W_w`：付费模式下界 1、上界 `threshold-1`；免运模式下界 `threshold`、上界库存总量
  - `W_w -> P_p`：上界为库存，单位费用为 `perItemFee`（免运模式为 0）
  - `P_p -> T`：下界为需求量，上界无穷（允许超量凑门槛）
  - `T -> S`：回流边构成循环流
- 枚举完备，结果为**精确最优**；8 仓 12 商品单次求解约 200ms。

## 最优性下界

结果中的 `lowerBound` 是一个松弛下界：忽略固定费用与门槛的耦合，按商品用最低边际费率
（有门槛的仓按免运模式取 0）满足需求，并在不可能免运时加上最小启用费。
任何可行方案的费用都不低于该值，`totalCost - lowerBound` 即为最优间隙的保守估计。

## 使用

```ts
import { solveOrderSplitting } from "./src/solver";

const result = solveOrderSplitting(
  [
    { id: "A", baseFee: 10, perItemFee: 2, freeShippingThreshold: 5, stock: { 键盘: 10 } },
    { id: "B", baseFee: 3, perItemFee: 1, stock: { 键盘: 10 } },
  ],
  [{ productId: "键盘", quantity: 4 }],
);
// => { status: "optimal", totalCost: 0, packages: 1, extraUnits: 1, ... }
//    仓 A 发 5 件凑够免运门槛，运费 0，比仓 B 的 7 元更便宜
```

## 运行

需要 [Bun](https://bun.sh)（直接运行 TypeScript）：

```sh
bun test          # 单元测试 + 60 组随机暴力对拍
bun run src/demo.ts
```
