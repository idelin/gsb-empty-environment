# seam-solver

在两幅已对齐图像的重叠区上为每个像素选择来源（A 或 B），最小化

```
总费用 = Σ 来源代价  +  Σ 相邻像素来源不同的切换代价
```

边界像素可通过 `fixed` 固定来源。输出分割结果与费用明细。

## 方法

这是二元子模标记问题（Potts  pairwise 项），用 **s-t 最小割精确求解**
（Dinic 最大流，确定性遍历顺序）。在所有最优解中返回 **A-极大** 的那个
（残余网络中反向 BFS 得到的规范最小割），因此并列时确定性地偏向来源 A，
多次运行结果完全一致。

## 用法

```ts
import { solveSeam } from './src/seamSolver.ts';

const result = solveSeam({
  width: 2,
  height: 1,
  costA: [[2, 8]],        // costA[y][x]: 像素 (x,y) 取自 A 的代价
  costB: [[7, 1]],        // costB[y][x]: 像素 (x,y) 取自 B 的代价
  switchCost: 3,          // 相邻像素来源不同的统一惩罚
  // horizontalSwitch / verticalSwitch: 可选的逐边切换权重
  // fixed: [['A', null]] // 可选：固定边界来源
});
// result.labels -> [['A', 'B']]
// result.costs  -> { sourceCostA, sourceCostB, sourceCost, switchCost, switchCount, total }
```

输入校验：

- 负的或非有限的代价（来源/切换）抛出 `RangeError`；
- 形状不符（行数/列数、非整数尺寸、非法 `fixed` 值）抛出 `TypeError`；
- 无重叠（`width` 或 `height` 为 0）返回空分割与全零费用，不抛异常。

## 验证

`src/bruteForce.ts` 提供穷举参考解（枚举所有满足固定约束的 2^n 种标记），
`verifyOptimality()` 将精确解与穷举最小值比对。测试覆盖：

- 负输入 / 非有限输入 / 畸形输入的拒绝；
- 无重叠区域；
- 并列解的确定性（偏向 A 的规范选择、重复运行一致）；
- 小图上与穷举结果逐一核对（均匀/逐边切换权重、固定边界列、整数与浮点代价）。

## 运行

需要 Node ≥ 22.6（原生 TypeScript 类型擦除），无第三方依赖：

```sh
npm test          # node --test test/seamSolver.test.ts
npm run demo      # node examples/demo.ts
```
