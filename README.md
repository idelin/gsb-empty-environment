# spacetime-bvh

四维时空包围盒（3D + Time）BVH 树索引器，面向移动物体轨迹的时空检索。

将每条轨迹线段（两个采样点之间的直线运动）视为一个静态 4D AABB
`(x, y, z, t)`，自顶向下构建 BVH；查询时用「时间段 + 空间立方体」组成的
4D 盒子做剪枝遍历，毫秒级返回真正进入该时空区域的具体轨迹片段。

## 环境要求

- Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译、零依赖）

## 运行

```bash
npm test     # Node.js 内置测试（node:test）
npm run demo # 无人机巡航查询演示
```

## 用法

```ts
import { SpaceTimeBVH, queryBox, trajectoryToSegments } from './src/index.ts';

// 1. 轨迹折线 -> 轨迹段
const segments = trajectoryToSegments('UAV-001', [
  { x: -3000, y: 200, z: 150, t: Date.parse('2026-10-02T13:58:00+08:00') },
  { x:  3000, y: -300, z: 160, t: Date.parse('2026-10-02T14:12:00+08:00') },
]);

// 2. 构建 4D BVH 索引（也支持 insert 动态插入）
const bvh = new SpaceTimeBVH(segments);

// 3. 查询：14:00-14:10、保护区立方体、高度 >= 100m
const box = queryBox(
  Date.parse('2026-10-02T14:00:00+08:00'),
  Date.parse('2026-10-02T14:10:00+08:00'),
  -1000, 1000,   // x
  -1000, 1000,   // y
  100, Infinity, // z（高度 100 米以上）
);
const { segments: hits, stats } = bvh.query(box);
```

## 设计要点

- **4D AABB**：时间轴作为第四维，轨迹线段随时间扫过的范围即静态 4D 盒。
- **构建**：以最宽轴中位数切分自顶向下构建，O(n log n)。
- **动态插入**：按四维超体积增量最小原则下潜，叶子溢出即分裂并回贴包围盒。
- **剪枝**：节点 4D 包围盒与查询盒不相交则整棵子树一次比较丢弃。
- **精确过滤**：AABB 重叠只是粗判，`segmentEntersBox` 对候选做线段-盒子
  解析求交（逐轴求参数区间再取交），消除「空间在盒内但时间不符」等假阳性。
- **查询统计**：`stats` 返回访问节点数、候选测试数与耗时，便于验证剪枝效率。

## 实测性能（Apple Silicon，50,000 条轨迹段）

| 指标 | 数值 |
| --- | --- |
| 索引构建 | ~140 ms |
| 单次时空查询 | **~0.2 ms** |
| 剪枝率 | 候选 358 / 50,000（>99% 被剪枝） |

## 项目结构

```
src/aabb4.ts    4D AABB（相交/扩张/最宽轴/超体积）
src/segment.ts  轨迹段与线段-盒子精确求交
src/bvh.ts      BVH 树：构建 / 插入 / 查询 / 统计
examples/demo.ts 无人机巡航演示（14:00-14:10、高度>=100m、穿越保护区）
test/bvh.test.ts node:test 测试（正确性、剪枝率、性能、验收场景）
```
