# 三维线性轨迹段时空索引（TypeScript）

回放系统组件：找出**某段时间内经过指定空间盒的运动对象**，命中结果可解释——
每个命中都能追溯到具体的**轨迹版本**与**轨迹段**，并给出精确的**有理数命中时间区间**。

## 语义约定

- **时间左闭右开**：轨迹段时间域为 `[t0, t1)`，查询时间窗为 `[w0, w1)`。
  查询窗只触及段的右开端点（如窗口 `[0, t0)` 碰到段起点 `t0`）不算命中。
- **空间查询盒闭合**：`lo <= p <= hi`（各轴闭区间）。轨迹恰好擦边（切触盒面）
  在某瞬时接触盒子也算命中，产生一个退化点区间 `[t*, t*]`（`endInclusive = true`）。
- **零时长段非法**：`t0 === t1` 的段在插入时抛出 `InvalidSegmentError`；
  `t0 > t1` 同样拒绝。坐标与 tick 必须是整数（`number` 或 `bigint`）。
- **精确相交**：逐轴求解线性不等式 `lo <= p0 + d·(t−t0)/L <= hi`，
  用 BigInt 有理数（`Fraction = { n, d }`，既约、`d > 0`）精确表示进入/离开时刻，
  再与 `[t0, t1)` 和查询窗求交。**不是**四维包围盒重叠就直接判命中——
  BVH 只产生候选，最终结果一律经过精确相交复核（测试中有专门的
  “4D AABB 重叠但精确不相交”用例）。
- **区间表示**：`{ start, end, endInclusive }`。`endInclusive = false` 表示
  右开（命中延续到段尾或窗尾）；`true` 表示右闭（在 `end` 时刻离开盒子，
  含端点；点命中时 `start === end`）。

## 目录结构

```
src/fraction.ts    精确有理数运算（BigInt）
src/geometry.ts    段/盒/时间窗类型与校验（零时长段在此拒绝）
src/intersect.ts   段-盒精确相交（有理时间区间）+ positionAt
src/merge.ts       同对象多段命中合并为不相交区间（保留来源）
src/bvh.ts         4D (x,y,z,t) BVH，候选剪枝 + 访问统计
src/index.ts       SpatioTemporalIndex：版本化存储、查询、统计、格式化
src/verifier.ts    独立验证器（见下）
scripts/build.mjs  类型擦除构建（见“构建与测试”）
test/              Node 测试（自带微型 harness，Node 16 可跑）
examples/demo.mjs  端到端示例
```

## 构建与测试

源码是标准 TypeScript（仅使用可擦除语法子集）。本环境没有 `tsc`，
因此 `scripts/build.mjs` 用 tokenizer 级别的类型擦除器把 `src/*.ts`
编译为 `dist/*.js`（保留行号，生成后即做 import 自检）：

```sh
npm run build     # src/*.ts -> dist/*.js
npm test          # 构建 + 运行全部测试
npm run demo      # 构建 + 端到端示例
npm run typecheck # 若本机装有 TypeScript，可用 tsc --noEmit 做完整类型检查
```

## 输入

### 轨迹段 `SegmentInput`

```json
{
  "objectId": "ship-1",
  "segmentId": "s1",
  "t0": 0, "t1": 10,
  "p0": { "x": 0, "y": 0, "z": 0 },
  "p1": { "x": 30, "y": 0, "z": 0 }
}
```

`t0/t1` 与坐标为整数（`number` 或 `bigint`），`t0 < t1`，时间域 `[t0, t1)`，
位置从 `p0` 到 `p1` 线性插值。

### 索引操作

| 方法 | 说明 |
| --- | --- |
| `addSegment(seg)` / `addSegments(segs)` | 加入工作集（`segmentId` 不得重复） |
| `updateObject(objectId, segs)` | 整体替换某对象的轨迹（先校验再替换） |
| `removeObject(objectId)` | 从工作集移除对象，返回移除段数 |
| `commit()` | 把工作集冻结为新版本，返回版本号（1 起递增） |
| `query(box, window, options?)` | 查询；`options.version` 缺省为最新版本 |
| `getVersionSegments(v)` / `listVersions()` | 版本内容访问 |

更新轨迹 = `updateObject(...)` + `commit()`，生成新版本；旧版本快照
不可变、仍可查询（`{ version: 旧版本号 }`）。

### 查询输入

```js
idx.query(
  { lo: { x: 10, y: -5, z: -5 }, hi: { x: 20, y: 5, z: 5 } }, // 闭合空间盒
  { t0: 0, t1: 20 },                                          // 左闭右开时间窗
  { version: 1 }                                              // 可选，缺省最新
);
```

## 输出 `QueryResult`

```json
{
  "version": 1,
  "window": { "t0": 0n, "t1": 20n },
  "box":    { "lo": {...}, "hi": {...} },
  "objects": [
    {
      "objectId": "ship-1",
      "version": 1,
      "intervals": [
        {
          "start": { "n": 10n, "d": 3n },
          "end":   { "n": 20n, "d": 3n },
          "endInclusive": true,
          "sources": [
            { "segmentId": "s1", "interval": { "start": ..., "end": ..., "endInclusive": true } }
          ]
        }
      ]
    }
  ],
  "stats": { ... }
}
```

- `objects[].intervals`：该对象命中的**不相交**时间区间（相邻/重叠的段命中已合并，
  如 `[3,10)` 与 `[10,20)` 合并为 `[3,20)`）。
- `sources`：合并区间的**来源**——每段的段 ID 与该段各自的命中子区间，
  配合 `version` 即可完整解释“结果来自哪段轨迹的哪个版本”。
- 有理数用 `{ n, d }`（BigInt）表示；`formatQueryResult(result)` 可把结果
  转成 JSON 友好形式（分数渲染为 `"10/3"` 字符串）。

## 索引访问统计 `stats`

| 字段 | 含义 |
| --- | --- |
| `version` | 本次查询使用的轨迹版本 |
| `totalSegments` | 该版本段总数 |
| `nodesVisited` / `nodesPruned` | BVH 访问 / 剪枝的节点数 |
| `leavesVisited` | 访问的叶子数 |
| `candidateSegments` | 通过 4D 包围盒测试、进入精算的段数 |
| `prunedSegments` | 被剪枝的段数（`totalSegments - candidateSegments`） |
| `exactTests` | 精确相交次数（等于 `candidateSegments`） |
| `hitSegments` | 精算确认命中的段数 |

`index.lastQueryStats` 保存最近一次查询的统计。

## 独立验证器 `verifyQueryResult(index, result)`

验证器**不信任**索引，对返回结果做如下独立复核（而不是只检查对象存在）：

1. 绕过 BVH，枚举该版本全部段，用**独立编写的**有理求解器
   （`verifier.ts` 内的 `recomputeHitInterval`，与 `intersect.ts` 不同实现）重算命中区间；
2. 几何抽查：用 `positionAt` 计算区间起点（及闭端点）时刻的位置，必须在盒内；
3. 重新合并并与返回的合并区间逐区间比对（起点/终点/开闭性）；
4. 按来源段 ID 找回原始段，复算来源子区间，并检查来源无缝覆盖合并区间；
5. 检查合并区间有序且两两不相交；
6. 校验访问统计的内部一致性（如 `hitSegments` 等于来源总数）。

返回 `{ ok, errors, checkedObjects, checkedSources }`。测试包含 300 组随机
模糊用例（静止段、穿越、擦边、薄盒等）以及 7 类篡改用例（平移端点、
删除来源、丢对象、幻影对象、翻转开闭、换段 ID、改统计），验证器均能识别。

## 测试覆盖的边界情形

- 静止段（盒内/盒外/恰在盒面上）
- 零时长段、反向时间段、非整数坐标/tick 的非法输入
- 擦边切触（点命中）与“差一点都不相交”
- 查询窗只触及段右开端点、命中区间被查询窗裁剪
- 命中终点恰在窗起点（闭盒 → 点命中）与恰在窗终点（右开 → 排除）
- 轨迹穿越盒子且两端点都在盒外（内部区间，含分数时刻如 `11/3..20/3`）
- 4D 包围盒重叠但精确不相交（BVH 候选被精算否决，统计可查）
- 多版本：更新后旧快照查询结果不变
