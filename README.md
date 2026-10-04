# 合单拣货规划器（Batch Picking Planner）

在小型通道图上，对 **≤12 个拣货项** 精确求解合单拣货方案：多个订单可共用货位与车次，
车容量有限，订单被拆到多趟会产生拆单成本。目标为 **总行走距离 + 拆单成本最小**。

## 运行

需要 Node ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译）：

```bash
npm run demo          # 默认：车容量 6，拆单成本 5
npm run demo 6 0      # 自定义：车容量 6，拆单成本 0
npm test              # 单元测试 + 与暴力枚举的交叉验证
```

## 模型

- **输入**：通道图（节点 = 货位/交叉口/出库口，无向边带距离）、拣货项列表
  （订单号、货位、数量）、车容量 `capacity`、拆单成本 `splitCost`。
- **拆单成本**：一个订单被分到 `t` 趟，计 `(t - 1) × splitCost`。
- **输出**：每趟的完整行走路径（出库口出发并返回）、载荷、每个停靠货位上
  **按订单区分的拣货数量**，以及行走/拆单/总成本。

## 算法（精确求解）

1. **Floyd-Warshall** 求通道图全源最短路并记录路径（`src/graph.ts`）。
2. **Held-Karp TSP**：对所有货位子集求最优往返路线，`O(2^m · m²)`（`src/solver.ts`）。
3. **拣货项子集 DP**：`f[mask] = min f[mask∖s] + 行走(s) + splitCost·|订单(s) ∩ 订单(mask∖s)|`，
   只枚举满足容量约束的子集 `s`，整体 `O(3^n)`，n=12 时约 50 万次转移，毫秒级完成。

## 文件

- `src/types.ts` — 数据模型
- `src/graph.ts` — 最短路表与路径还原
- `src/solver.ts` — 精确求解器 `planPicking(graph, items, params)`
- `src/format.ts` — 方案文本格式化
- `src/demo.ts` — 示例通道图 + 12 个拣货项演示
- `test/solver.test.ts` — 含 60 个随机实例对暴力枚举（集合划分全枚举）的交叉验证
