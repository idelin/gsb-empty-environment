# grid-path-rational

网格路径计算工具（TypeScript，零依赖）。地图成本为**非负有理数**（BigInt 精确运算，
`1/3 + 1/6` 精确等于 `1/2`），启发函数由调用方提供。

## 特性

- **允许不一致启发的 A\***：节点关闭后仍可被重新打开（`reopens` 计数），
  只要启发可采纳即保证最优。
- **未证最优标记**：调用方通过 `admissible` 声明启发是否可采纳。若声明不可采纳，
  搜索会继续走到开列表耗尽（退化为带剪枝的 Dijkstra），结果仍正确但
  `provenOptimal` 恒为 `false`；`exhausted` 表示搜索空间已被完全探索。
- **路径与下界**：输出最优可行路径（上界 `cost`）与经认证的下界 `lowerBound`
  （可采纳时取开列表最小 `f`，否则取最小 `g` —— 成本非负所以仍然有效）。
- **预算中断保留可行上界**：`maxExpansions` 限制扩展次数；目标一旦**生成**即
  记录为现任解（incumbent），中断时返回当前可行上界与下界。
- **与 Dijkstra 核对**：测试覆盖零成本环、启发高估、提前碰到目标三个反例，
  以及随机有理数成本网格的交叉验证。

## 运行

需要 Node >= 22.6（利用原生类型擦除直接运行 `.ts`，推荐 23.9+）：

```sh
node --test          # 运行测试
node src/cli.ts examples/map.json   # 运行示例
```

## API

```ts
import { Grid, astar, dijkstra, manhattanHeuristic } from './src/index.ts';

const grid = new Grid([
  [1, '1/3', 0],   // null 表示障碍；成本为进入该格的代价
  [null, 2, '1/6'],
]);

const result = astar(grid, { x: 0, y: 0 }, { x: 2, y: 1 }, {
  heuristic: { h: myHeuristic, admissible: true },  // 调用方提供并声明
  maxExpansions: 1000,                               // 可选预算
});
```

`AStarResult` 关键字段：

| 字段 | 含义 |
| --- | --- |
| `status` | `optimal` / `feasible` / `interrupted` / `infeasible` |
| `path` / `cost` | 最优可行路径及其精确成本（上界） |
| `lowerBound` | 最优值的下界（中断时用于界定间隙） |
| `provenOptimal` | 仅当启发声明可采纳且搜索正常终止时为 `true` |
| `exhausted` / `interrupted` | 开列表是否耗尽 / 是否因预算中断 |
| `reopens` | 已扩展节点被重新扩展的次数 |
| `firstGeneratedGoalCost` | 目标首次被生成时的成本（贪心提前退出会返回的值） |
| `firstGoalPopCost` | 目标首次被弹出时的成本（启发高估时可能非最优） |

## 反例（见 `test/astar.test.ts`）

- **零成本环**：零成本格子成环，严格改进更新保证终止且结果与 Dijkstra 一致。
- **启发高估**：`h(A)=10` 高估使搜索先以成本 7 弹出目标；继续搜索重开节点 B，
  最终得到最优 3，`provenOptimal=false`。
- **提前碰到目标**：可采纳但不一致的启发让目标首次生成时成本为 8，
  而最优为 6 —— 实现按“弹出”而非“生成”终止，结果正确。

## 说明

- 内部把 `h(goal)` 视为 0；可采纳性由调用方声明，库不做运行时验证。
- 启发不可采纳时搜索不会提前在目标处停止，而是耗尽开列表，因此结果数值上
  仍是最优的，只是按约定不标记为已证最优。
