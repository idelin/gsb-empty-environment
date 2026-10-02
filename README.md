# dynamic-bvh

AABB 动态 BVH（层次包围盒）树，TypeScript 实现，零依赖，同时支持 3D 与 2D 场景。

## 特性

- **SAH 最优二分**：批量构建采用表面积启发式（Surface Area Heuristic），沿三个轴做 16 桶（binning）扫描，选取期望求交代价最小的分割平面，得到接近最优的平衡树。
- **动态插入 / 删除**：插入沿"包围盒表面积增量最小"的路径下降后向上重拟合（refit）；删除时坍缩父节点并重拟合祖先，树始终保持结构有效。
- **射线求交（Ray Cast）**：栈式遍历 +  slab 法包围盒测试，按进入距离近者优先展开；一旦子节点包围盒的进入距离超过当前最近命中即整棵子树剪枝。
- **维度通用**：`AABB` 基于数组实现，2D（矩形）与 3D（三角形等）共用同一套建树 / 求交逻辑。

## 运行

需要 Node.js ≥ 22.6（使用内置 TypeScript 类型擦除，无需编译）。

```bash
npm test     # Node.js 内置测试运行器 (node:test)
npm run demo # 1000 三角形 + 100 射线的验收样例
```

## 运行样例（`npm run demo`）

```
=== Dynamic BVH demo ===
triangles:            1000
rays:                 100
build time:           13.20 ms
raycast total time:   1.09 ms (0.011 ms/ray)
rays that hit:        7/100
leaf tests (BVH):     142
leaf tests (brute):   100000
leaves skipped:       99.86%
nearest-hit matches:  yes (100/100)
ACCEPTANCE PASSED
```

100 条射线对 1000 个三角形，暴力法需 100,000 次三角形求交；BVH 仅实际测试 142 个叶子（跳过 99.86%），总耗时约 1 ms，且每条射线的最近命中与暴力法逐一核对一致。

## 测试结果（`npm test`，13 个用例全部通过）

```
✔ surfaceArea: 3D box
✔ surfaceArea: 2D box degenerates to perimeter
✔ union contains both operands
✔ overlaps detects intersection and separation
✔ rayHit: slab test entry distance and miss
✔ rayHit: origin inside box returns t=0
✔ build: tree contains all items and validates structurally
✔ build: SAH produces a balanced tree (height ~ O(log n))
✔ insert/remove: tree stays valid and size-consistent
✔ insert/remove: query results match linear scan after churn
✔ raycast: matches brute-force nearest hit on 3D triangle scene
✔ raycast: works in 2D with rectangle payloads
✔ acceptance: 1000 triangles, 100 rays, >95% leaves skipped, ms-level
ℹ tests 13  ℹ pass 13  ℹ fail 0
```

## 代码结构

- `src/aabb.ts` — 维度通用的轴对齐包围盒：并集、表面积（3D 表面积 / 2D 周长）、重叠测试、slab 法射线测试。
- `src/bvh.ts` — BVH 核心：SAH 批量构建、动态插入/删除、射线遍历剪枝、AABB 重叠查询、结构校验与遍历统计（`stats.leafTests`）。
- `src/triangle.ts` — 三角形几何与 Möller–Trumbore 射线求交。
- `src/scene.ts` — 确定性随机场景/射线生成与暴力法基准。
- `src/demo.ts` — 验收样例（1000 三角形、100 射线、剪枝率与正确性核对）。
- `test/` — Node.js 内置测试（`node:test` + `node:assert`）。

## API 速览

```ts
import { BVH } from "./src/bvh.ts";

const tree = BVH.build(items);            // items: { aabb, data }[]
tree.insert({ aabb, data });              // 动态插入
tree.remove(item);                        // 动态删除（按 item 引用）
const hit = tree.raycast(origin, dir,     // 最近命中 { data, t } 或 null
  (data, aabb) => preciseIntersect(data)); // 返回距离 t 或 null
tree.query(box, (data) => { ... });       // 重叠查询
tree.stats.leafTests;                     // 叶子求交次数（剪枝率统计）
```
