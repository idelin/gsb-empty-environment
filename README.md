# spacetime-bvh

四维时空包围盒（3D 空间 + 1D 时间）BVH 树索引器，面向移动物体（无人机等）的
时空轨迹索引。每条轨迹被采样为折线段序列，每个线段以其 4D AABB
`(x, y, z, t)` 入库；查询时给出一个 4D 包围盒（空间立方体 × 时间窗口），
BVH 自顶向下剪枝，毫秒级返回所有进入该时空区域的轨迹线段及其精确进出片段。

## 环境要求

- Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤）
- 测试使用 Node 内置 `node:test` + `node:assert`，零第三方依赖

## 快速开始

```bash
npm test        # 运行 node:test 测试套件
npm run demo    # 运行无人机巡航轨迹演示
```

## API 概览

- `AABB4` — 4D 轴对齐包围盒：`intersects` / `merge` / `unionVolume` / `volume` 等
- `SpacetimeBVH`
  - `build(segments)` — 批量构建平衡树（预计算质心键 + 索引排序，O(n log² n)）
  - `insert(segment)` / `insertTrajectory(id, points)` — 增量插入
    （最小体积扩张启发式下降 + 叶子分裂）
  - `query(box, { precise? })` — 返回与查询盒重叠的所有线段
  - `queryFragments(box)` — 额外做 4D Liang–Barsky 精确裁剪，返回每个线段
    在盒内的精确进出点与进出时刻
- `buildSegments(id, points)` / `clipSegmentToBox(seg, box)` — 轨迹工具函数

## 验收场景（examples/demo.ts）

500 架无人机巡航 1 小时（每秒 1 个采样点，共 180 万条线段），查询
**14:00–14:10 之间、高度 100 米以上、穿越核心保护区**（2km × 2km 空域柱）
的所有轨迹段：

```
Bulk-built index: 500 trajectories / 1800000 segments in 4431.2 ms (tree depth: 19)
Incrementally inserted 3600 segments of a late drone in 10.7 ms (tree size now 1803600)

Query: 14:00-14:10, altitude >= 100 m, inside protected zone
Query time: 2.998 ms | nodes visited: 4387, subtrees pruned: 1272, segments tested: 6295
Matching fragments: 3134

drone-444 seg#2160: enter (1000, 359, 196 m) @ 14:06:00 -> exit (1000, 359, 196 m) @ 14:06:00
drone-37 seg#2160: enter (1000, 259, 222 m) @ 14:06:00 -> exit (1000, 259, 222 m) @ 14:06:00
drone-148 seg#2160: enter (1000, 309, 144 m) @ 14:06:00 -> exit (1000, 309, 144 m) @ 14:06:00
...
Distinct drones inside zone: 11
```

180 万条线段中仅访问 4387 个节点、精确测试 6295 条线段（0.35%），
查询耗时约 **3 ms**。

## 测试结果（node --test）

```
✔ AABB4 (2.989417ms)
✔ clipSegmentToBox (0.4295ms)
✔ SpacetimeBVH (50.477416ms)
ℹ tests 10
ℹ pass 10
ℹ fail 0
```

覆盖：4D 包围盒相交/合并、线段精确裁剪（含 AABB 假阳性剔除）、
批量构建 vs 暴力扫描一致性（随机数据）、增量插入 vs 批量构建一致性、
时间窗 + 高度过滤的验收场景、大规模查询剪枝与毫秒级性能、空树边界。

## 目录结构

```
src/aabb4.ts       4D AABB 数据结构与运算
src/trajectory.ts  轨迹点/线段模型 + 4D Liang–Barsky 裁剪
src/bvh.ts         BVH 树：批量构建、增量插入、范围查询（含剪枝统计）
src/index.ts       公共导出
examples/demo.ts   无人机巡航验收演示
test/bvh.test.ts   node:test 测试套件
```
