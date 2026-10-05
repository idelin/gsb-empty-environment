# Spatiotemporal Trajectory Segment Index

TypeScript 实现的三维线性轨迹段时空索引：回放查询“哪些运动对象在
某段时间内经过指定空间盒”，并给出每段命中的精确有理数时间区间与
来源（段 ID + 轨迹版本）。

纯 TypeScript（Node ≥ 22.6 的 type stripping 直接运行，无需编译、
无第三方依赖）。

## 运行

```sh
npm test     # node --test（33 个用例）
npm run demo # 端到端示例，打印 JSON 结果与访问统计
```

## 数据模型（输入）

```ts
interface Segment {
  objectId: string;
  segmentId: string;
  t0: number;      // 起始 tick（含），安全整数
  t1: number;      // 结束 tick（不含），必须 t1 > t0
  from: Vec3;      // 整数位置 p(t0)
  to: Vec3;        // 整数位置 p(t1)
}
// t ∈ [t0, t1) 时位置 p(t) = from + (to - from)·(t - t0)/(t1 - t0)

interface SpatioTemporalQuery {
  time: { t0: number; t1: number };  // 半开 [t0, t1)
  box: { min: Vec3; max: Vec3 };     // 闭合空间盒
}
```

- 时间一律左闭右开；空间盒闭合（贴面/擦边算命中）。
- 零时长或负时长段（`t1 <= t0`）为非法，`updateTrajectory` 抛
  `InvalidSegmentError`，且不产生新版本。
- 同一对象一次更新内的 `segmentId` 必须唯一。

## API 与输出

```ts
const idx = new SpatioTemporalIndex();
const v = idx.updateTrajectory(objectId, segments); // 返回新的全局版本号
const res = idx.query(query, version?);             // 缺省查最新版本
const ref = idx.queryBruteForce(query, version?);   // 全段精算参考实现
```

```ts
interface QueryResult {
  version: number;              // 本次查询使用的快照版本
  objects: ObjectResult[];      // 仅含有命中的对象
  stats: QueryStats;            // 索引访问统计
}

interface ObjectResult {
  objectId: string;
  version: number;              // 该对象实际引用的轨迹版本
  intervals: MergedInterval[];  // 不相交、按时间排序的命中区间
}

interface MergedInterval {
  lo: Rational; hi: Rational;   // 有理数端点，如 "25/3"
  hiOpen: boolean;              // hi 是否被半开时间边界排除
  sources: { segmentId: string; interval: TimeInterval }[]; // 来源段
}
```

- 同一对象多段的命中合并为不相交区间，但每个合并区间保留全部来源
  段及其各自的精确时间区间（可解释性）。
- 退化命中（轨迹仅在某一时刻擦过盒）表示为 `lo == hi, hiOpen=false`
  的单点区间；只触及右开时间端点（段 `t1` 或查询 `t1`）不算命中。
- 每次 `updateTrajectory` 生成新的全局版本；旧版本快照永久保留，
  `query(q, oldVersion)` 对每个对象解析“≤ 该版本的最近一次更新”。

## 精确相交算法

对每段逐轴求解 `(min−p0)·T/d ≤ t−t0 ≤ (max−p0)·T/d`（`d<0` 时翻转），
用 BigInt 有理数（`src/rational.ts`）求三轴区间与
`[max(t0,qt0), min(t1,qt1))` 的精确交集。BVH（`src/bvh.ts`，4D 包围盒）
只做候选剪枝，绝不直接判定命中；所有候选都经过
`src/intersect.ts` 的有理数精算。

## 索引访问统计

每次查询返回并在索引上累计（`idx.getStats()`）：

| 字段 | 含义 |
| --- | --- |
| `bvhNodesVisited` | 访问的 BVH 节点数 |
| `bvhLeavesVisited` | 命中的叶子数 |
| `candidateSegments` | 剪枝后进入精算的段数 |
| `exactIntersectionTests` | 精算次数（= 候选数） |
| `hitSegments` | 精确命中的段数 |
| `totalSegments` | 快照内段总数 |

## 验证

- `queryBruteForce`：跳过 BVH、对快照内全部段精算的参考实现；
  随机化测试（静止段、退化盒、跨盒、擦边等）逐位比对两者结果。
- `src/verify.ts` 的 `verifyQueryResult` 是独立验证器：用另一条
  代码路径（参数 s∈[0,1) 的 slab 法）从结果引用的来源段复算时间
  区间，并检查版本解析、来源存在性、合并区间等于来源之并、区间
  不相交、以及对快照全段的完备性，而非只检查对象是否存在。

## 文件布局

```
src/rational.ts    BigInt 有理数
src/types.ts       输入输出类型
src/intersect.ts   精确段-盒相交（有理时间区间）
src/bvh.ts         4D BVH 候选剪枝
src/merge.ts       命中区间合并（保留来源）
src/index.ts       版本化索引 + 暴力参考实现
src/verify.ts      独立验证器
test/              node:test 测试
examples/demo.ts   端到端示例
```
