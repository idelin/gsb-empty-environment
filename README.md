# 接缝求解器（Seam Solver）

在两幅已对齐图像的重叠区为每个像素选择来源（图像 A 或图像 B），
最小化 **来源代价之和 + 相邻像素来源切换代价之和**：

```
E(L) = Σ_p D_p(L_p) + Σ_(p,q)∈N V_pq · [L_p ≠ L_q]
```

这是次模二值能量，通过 s-t 最小割（Dinic 最大流）**精确求得全局最优解**。
边界上的固定来源约束以无穷大容量边实现（硬约束）。

## 运行环境

- Node.js ≥ 23.6（内置 TypeScript 类型擦除，可直接运行 `.ts`），或
- 任意 Node + `tsc` 自行编译（`tsconfig.json` 已备好，`erasableSyntaxOnly` 兼容）。

```bash
node --test 'test/*.test.ts'   # 运行测试
node examples/demo.ts          # 运行示例
```

## API

```ts
import { solveSeam } from './src/seam.ts';

const result = solveSeam({
  width, height,
  costA,                  // [height][width]，像素取自 A 的代价，≥ 0
  costB,                  // [height][width]，像素取自 B 的代价，≥ 0
  switchCostHorizontal,   // 可选，[height][width-1]，水平相邻来源不同的代价
  switchCostVertical,     // 可选，[height-1][width]，垂直相邻来源不同的代价
  fixed,                  // 可选，[{ x, y, label }]，label: 0 = A, 1 = B
}, { tieBreak: 'A' });    // 可选：'A' | 'B' | 'none'（默认 'none'）
```

返回 `labels`（分割结果，`0 = A / 1 = B`）与费用明细：
`sourceCostA`、`sourceCostB`、`switchCostHorizontal`、`switchCostVertical`
及小计 `sourceCost`、`switchCost`、`totalCost`（即能量最小值）。

## 行为约定

- **输入校验**：负值、NaN/Infinity、维度不符、越界或互相冲突的固定约束
  均抛出 `SeamInputError`。
- **无重叠**：`width` 或 `height` 为 0 时返回空分割与全零费用，不视为错误。
- **并列解**：求解器对相同输入完全确定（同一实现重复求解结果一致）；
  `tieBreak` 通过对对立标签施加 1e-9 量级的微偏置选出规范解，
  报告的费用始终按原始（未加偏置）代价核算。

## 测试

`test/seam.test.ts` 覆盖：

- 负代价 / 非有限值 / 维度不符 / 冲突约束的拒绝；
- 无重叠（0×0、0×N）行为；
- 固定约束被严格遵守；
- 并列解的确定性与 `tieBreak` 规范解；
- 费用明细自洽（`total = source + switch`，与 `evaluateCosts` 独立复核一致）；
- **小图穷举核对**：1×1 至 4×3 的随机实例（含随机固定约束）枚举全部
  2^(w·h) 种标注，验证求解器费用等于穷举最优值。
