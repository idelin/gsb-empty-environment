# ccd2d — 二维轴对齐矩形连续碰撞检测（精确有理数）

针对「每帧只看当前位置会漏掉两帧之间穿过的障碍」的问题，本库在闭时间区间
`[0, T]` 内对匀速运动的轴对齐闭矩形做**连续**碰撞检测（CCD），全程使用
BigInt 分数运算，输出每对碰撞对象的**精确接触时间闭区间**，不做任何离散采样。

## 算法

1. **宽相（候选生成）**：对每个矩形求 `[0, T]` 上的扫掠包围盒（swept AABB），
   插入空间哈希网格覆盖的所有格子（跨多格、负坐标均正确处理）；同一格子中的
   物体对经扫掠包围盒相交过滤后成为候选。候选对按 `(i, j)` 去重，
   **同一对只产生一次**（网格边界重复枚举被消除）。
2. **窄相（精确求交）**：每对候选按轴建立有理线性不等式
   `(v_a - v_b) * t <= (b_max - a_min)` 等四条，逐轴求交再与 `[0, T]` 求交。
   零相对速度退化为常量可行性判断；接触（区间端点相等）算碰撞；
   初始重叠从 `t = 0` 开始；`t = T` 才接触也会返回 `[T, T]`。
3. **独立核对**：每次查询额外做 O(n²) 全配对精算，与候选结果逐对比较，
   报告 `candidatePairs`（候选数）、`exactPairs`（精算数）与 `verified`
   （候选无漏项且无多项）。

## 对象更新与稳定 ID

- `upsert / remove` 只进入待处理队列，`commit()` 时**批量生效**；
  查询永远看到的是最近一次 commit 的状态。
- ID 一经删除即永久退役：再次 `upsert` 同一 ID 会抛错，
  防止已删除对象从旧格子「复活」。宽相网格每次查询时从存活对象重建。

## 使用

```bash
npm run build        # 编译 TypeScript -> dist/（环境无 tsc，使用自带擦除式构建器）
npm test             # 构建并运行全部 Node 测试
node bin/ccd.js scene.json            # 从文件读取场景
cat scene.json | node bin/ccd.js      # 从标准输入读取
node bin/ccd.js scene.json --cell-size 1/16   # 覆盖网格尺寸
```

场景 JSON（数值可以是整数或 `"p/q"` 精确分数字符串）：

```json
{
  "T": "3",
  "cellSize": "2",
  "objects": [
    {"id": "a", "x": 0, "y": 0, "w": 1, "h": 1, "vx": 0, "vy": 0},
    {"id": "b", "x": 2, "y": 0, "w": 1, "h": 1, "vx": -1, "vy": 0}
  ],
  "updates": [
    {"upsert": [{"id": "c", "x": 5, "y": 0, "w": 1, "h": 1, "vx": -2, "vy": 0}],
     "remove": ["a"]}
  ]
}
```

输出包含 `candidatePairs`、`exactPairs`、`verified` 与每对碰撞的
`interval: [start, end]`（闭区间，精确分数）。

## 库 API

```ts
import { World } from './src/world.js';

const world = new World();
world.upsert({ id: 'a', x: 0, y: 0, w: 1, h: 1, vx: 0, vy: 0 });
world.upsert({ id: 'b', x: 2, y: 0, w: 1, h: 1, vx: -1, vy: 0 });
world.commit();
const result = world.query(3, undefined);  // T=3, 网格尺寸用默认启发式
// result.collisions == [{a:'a', b:'b', start:1, end:3}]
```

## 构建说明

环境没有 tsc / 网络，因此 `scripts/build.mjs` 是一个针对本仓库
「可擦除语法」子集的最小 TypeScript 擦除器（源码约定见文件头注释），
产物为 ESM JS，并用 `vm.SourceTextModule` 做语法校验。若环境有 tsc，
也可以直接用 `tsconfig.json` 编译 `src/` 与 `test/`。

## 测试覆盖

- 相同运动（零相对速度）下重叠 / 不重叠
- 极窄相交时间窗（宽度 1e-9 的精确分数端点）
- 网格边界重复枚举去重、跨多个网格的快速物体
- `t = T` 恰好接触、初始重叠、负坐标、零尺寸点矩形
- 批量更新可见性、稳定 ID 防复活、每对只返回一次
- 随机场景下宽相候选 vs 全配对精算的一致性核对
