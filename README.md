# 时空栅格动态连通性与洪水蔓延模拟器

基于 TypeScript 的时空二维栅格状态演化仿真内核：输入数字高程模型（DEM）、
渗透率栅格与随时间演进的暴雨时序，在离散 tick 下模拟水流沿坡度漫延浸润
过程，支持在**任意历史时刻**查询积水连通区域边界多边形与被水体阻断的
道路通行断点列表。零第三方依赖，使用 Node.js 内置测试框架验证。

## 环境要求

- Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TypeScript）

## 快速开始

```bash
npm test        # 运行 Node.js 内置测试（node:test）
npm run demo    # 运行洪水蔓延样例
```

## 仿真模型

每个 tick 依次执行三个阶段（体积守恒）：

1. **降雨**：`depth += rainfall[tick]`（mm → m），时序可任意长度；
2. **入渗**：`depth -= infiltrationRate`（m/tick，逐单元渗透率），下限 0；
3. **漫延**：按水面高程（高程 + 水深）差向 8 邻域低处重新分配水量，
   通量按距离衰减、按高差加权，分配比例受 `flowFactor ∈ (0, 0.5]` 限制，
   保证数值稳定与体积守恒。

每个 tick 结束保存深度快照，可通过 `depthAt(t)` / `floodedMaskAt(t)` /
`cellStateAt(t)` 回溯任意历史时刻状态。

## 核心 API

| 模块 | 说明 |
| --- | --- |
| `src/dem.ts` | DEM / 栅格构建与邻域工具 |
| `src/simulator.ts` | `FloodSimulator`：tick 演化内核 + 历史快照 |
| `src/connectivity.ts` | 4 连通区域提取、边界多边形追踪（含孔洞、共线点简化） |
| `src/roads.ts` | 道路中心线采样与断点检测（连续阻断段合并） |

### 示例

```ts
import { FloodSimulator, demFromArray, floodedRegions, findRoadBreakpoints } from './src/index.ts';

const dem = demFromArray(elevations, /* cellSize */ 20);
const sim = new FloodSimulator({
  dem,
  infiltration,                    // 可选，渗透率栅格 m/tick
  rainfall: { mmPerTick: [4, 8, 15, 25, 35, 30, 18, 10, 5, 2, 0, 0] },
  floodThreshold: 0.15,            // 积水 >15cm 视为淹没
});
sim.run(12);

// 任意历史时刻的连通淹没区域边界多边形
const mask = { rows: dem.rows, cols: dem.cols, data: sim.floodedMaskAt(8) };
const regions = floodedRegions(mask, sim.depthAt(8), dem.cellSize);

// 任意历史时刻的道路断点列表
const breakpoints = findRoadBreakpoints(roads, sim.depthAt(8), 8, {
  rows: dem.rows, cols: dem.cols, cellSize: dem.cellSize, floodThreshold: 0.15,
});
```

## 边界多边形追踪

对连通淹没单元集合，提取“淹没/非淹没”交界的有向单元边，边链自然闭合成
环；以有向面积最大者为外环、其余为孔洞，并去除共线中间格点，输出紧凑
多边形顶点序列（栅格坐标系，单位米）。

## 道路断点判定

道路以折线表示，按 `cellSize/2` 间距采样中心线；采样点落入水深超过阈值
的单元即为阻断。同一道路上相邻的阻断采样点合并为一个断点（取阻断段中点），
并标注其所属连通淹没区域 id。

## 样例输出（`npm run demo`）

30x30 栅格（单元 20m）中部河谷场景，12 tick 暴雨时序：

```
=== 逐 tick 淹没统计 ===
tick | 降雨mm | 淹没单元 | 水体体积m³
   6 |     30 |        2 | 33528.0
   7 |     18 |       31 | 38496.0
   8 |     10 |       67 | 40584.0
   9 |      5 |      120 | 40872.0
  12 |      0 |      124 | 37075.8

=== tick=8 道路断点（共 4 处）===
道路[滨河大道] 断点@(260,210) 单元(10,13) 水深 0.17 m 区域#0
道路[滨河大道] 断点@(320,210) 单元(10,16) 水深 0.17 m 区域#0
道路[环城快速路] 断点@(268,449) 单元(22,13) 水深 0.17 m 区域#0
道路[环城快速路] 断点@(323,468) 单元(23,16) 水深 0.17 m 区域#0
```

## 测试结果

18 个用例全部通过，覆盖：降雨累积、入渗、坡度漫延与体积守恒、历史快照
回溯、连通分量、边界多边形（单格/区块/环形孔洞/L 形）、道路采样与断点、
端到端集成。

```
# tests 18
# pass 18
# fail 0
```
