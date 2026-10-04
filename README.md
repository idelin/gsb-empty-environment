# 有成本的离散属性决策树求解器

给定一组候选对象，每个对象由若干**离散属性**描述；每次询问一个属性需要支付该属性的**成本**。求解器构造一棵**自适应询问树**，使得区分候选的**最坏总成本最小**（minimax）。候选数最多 18 个。

- 属性完全相同（无法区分）的候选会保留为**等价组**，作为叶子整体输出，不会强行拆分。
- 每个分支都输出该分支剩余的候选集。
- 树是**真正自适应**的：不同分支的下一个问题可以不同。求解器在所有自适应树中做了全局最优搜索，而不是固定一个问序再声称最优（测试中会验证：示例 `examples/adaptive.json` 的最优固定问序成本为 3，而自适应最优为 2）。

## 算法

对候选子集做位掩码 DP（子集即状态，记忆化搜索）：

```
solve(S) = 0                                          若 S 中所有候选签名相同（一个等价组）
solve(S) = min_a [ cost(a) + max_G solve(G) ]         否则，对所有能分裂 S 的属性 a
           其中 G 为 a 在 S 内按取值划分出的各组
```

最坏情况下一条根到叶路径的总成本 = 各次询问成本之和，因此取 `max` 而非期望。`min` 遍历所有属性，问题顺序完全由 DP 按子集决定，天然支持不同分支问不同属性。

复杂度：状态数只含可达子集（最坏 O(3^n)，n ≤ 18 时实际输入下很快），每个状态 O(m·n)。

## 输入格式

```json
{
  "attributes": [{ "name": "color", "cost": 1 }],
  "candidates": [{ "name": "A", "values": { "color": "red" } }]
}
```

属性值可以是字符串 / 数字 / 布尔 / null；每个候选必须给出所有属性的值；成本为 ≥ 0 的有限数。

## 使用

```bash
# 运行（本仓库附带与 src/ 逐行对应的 dist/ 编译产物，可直接用 node 运行）
node dist/cli.js examples/adaptive.json          # 文本树
node dist/cli.js examples/adaptive.json --json   # JSON 树
cat problem.json | node dist/cli.js              # 从 stdin 读

# 测试（含与独立暴力实现的随机交叉验证、固定问序对比、18 候选上界等）
npm test

# 如有 TypeScript 编译器，可自行重建 dist/
npm run build
```

## 作为库调用（TypeScript）

```ts
import { solveDecisionTree, formatDecisionTree } from './src/solver.js';

const result = solveDecisionTree(problem);
console.log(result.worstCaseCost);      // 最小最坏总成本
console.log(result.equivalenceGroups);  // 无法区分的等价组
console.log(formatDecisionTree(result));// 每个分支带候选集的文本树
```

## 文件

- `src/solver.ts` — 求解器（DP + 树重建 + 格式化 + 输入校验）
- `src/cli.ts` — 命令行入口
- `dist/` — 与 `src/` 对应的可运行 JS（当前环境无 tsc，故随附等价产物；测试直接跑在 dist 上）
- `test/solver.test.mjs` — 测试
- `examples/` — 自适应优于固定问序、等价组、成本权衡三个示例
