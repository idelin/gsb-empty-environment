# 时空栅格动态连通性与洪水蔓延模拟器

基于时空二维栅格的状态演化仿真内核（TypeScript，零依赖）。模拟随时间推移
地表淹没区域的动态扩散，考虑地形高程（DEM）、土壤渗透率与降雨量时态变化
序列；在离散 tick 下计算任意历史时刻的积水连通区域及其边界多边形，并输出
被水体阻断的道路通行断点列表。

## 运行环境

- Node.js >= 22.6（利用原生 TypeScript 类型擦除运行，无需编译、无第三方依赖）
- Node.js >= 23.6 时可省略 `--experimental-strip-types` 标志

## 快速开始

```bash
npm test        # 运行 Node.js 内置测试（node:test）
npm run demo    # 运行洪水蔓延样例
```

等价于：

```bash
node --experimental-strip-types --test test/*.test.ts
node --experimental-strip-types examples/demo.ts
```

## 模块结构

- `src/dem.ts` — 数字高程模型（DEM）栅格，米制高程 + 像元尺寸
- `src/rainfall.ts` — 暴雨时序数据：逐 tick 降雨帧，支持全域均匀或空间分布
- `src/simulator.ts` — 仿真内核 `FloodSimulator`：逐 tick 演化并快照历史
- `src/regions.ts` — 积水连通区域提取（4-连通 flood fill）与边界多边形追踪
- `src/roads.ts` — 道路折线栅格化（supercover）与通行断点检测

## 仿真模型

每个离散 tick 依次执行：

1. **降雨输入**：该 tick 的降雨帧（mm，随时间变化、可空间分布）叠加到积水深度场；
2. **渗透损失**：按每格渗透率（mm/tick）下渗；
3. **坡度漫流**：水沿 4-邻域按水面高程差（地形高程 + 积水深度，统一毫米）
   比例交换，Jacobi 快照更新；单对转移封顶为水面差的一半（防振荡），
   每格总流出按可用水深比例缩放（严格守恒，不产水、不透支）；
4. **开放边界**（可选 `openBoundary`）：边界格按 `flowCoeff` 比例向域外排水，
   模拟流域出口；默认封闭流域。

每个 tick 的深度场均被快照，可查询任意历史时刻 `depthAt(tick)` /
`floodedMaskAt(tick)` / 连通区域 / 道路断点。

## 使用示例

```ts
import { Dem, RainfallSeries, FloodSimulator, RoadNetwork, analyzeMask } from "./src/index.ts";

const dem = new Dem(elevationGrid, /*cellSize=*/10);           // DEM 栅格
const rain = RainfallSeries.uniform([8, 12, 15, 15, 10, 6, 3, 1], rows, cols);
const sim = new FloodSimulator(dem, rain, infiltrationGrid, {
  flowCoeff: 0.3,        // 每 tick 邻格水面差交换比例
  floodThreshold: 2,     // 积水判定阈值 (mm)
  openBoundary: true,    // 开放流域边界
});

sim.run(14);                   // 推进 14 个 tick
const regions = analyzeMask(sim.floodedMaskAt(8));             // tick=8 连通区域+边界多边形
const segments = roads.breakpointsAt(sim.depthAt(8), 8, 8);    // tick=8 道路断点
```

## 样例场景（examples/demo.ts）

16x16 流域（10m 像元），南北向河谷贯穿中央，谷地黏土低渗透、山脊高渗透；
8 tick 衰减暴雨 + 两条道路（R1 东西向横穿河谷，R2 沿河谷南行）。输出节选：

```
tick | rain(mm) | ponded(mm*cell) | flooded cells | regions
-----+----------+-----------------+---------------+--------
   1 |       8 |         1393.7 |          224 |       1
   4 |      15 |         9657.8 |          224 |       1
   8 |       1 |        11425.5 |          152 |       1
  14 |       0 |         9801.6 |           30 |       1

== Flooded regions & boundary polygons at tick 8 ==
region #0: 152 cells, 1 boundary loop(s), outer loop 12 vertices, area 15200 m^2
  outer boundary (m): (60,0) (60,10) (40,10) (40,20) (30,20) (30,160) ...

== Road breakpoints (water-blocked segments) per historical tick ==
tick  2: R1-cross-valley BLOCKED cells (9,1)->(9,14) count=14 maxDepth=41.3mm
tick  8: R1-cross-valley BLOCKED cells (9,4)->(9,11) count=8 maxDepth=325.6mm
tick 13: R1-cross-valley BLOCKED cells (9,7)->(9,8) count=2 maxDepth=392.7mm
tick 13: R2-valley-road BLOCKED cells (1,7)->(13,8) count=14 maxDepth=392.8mm
```

雨停后总水量单调下降（守恒），淹没区向河谷收缩；R1 断点随退水逐段恢复
通行，R2 沿谷路段持续阻断。

## 测试结果

21 个用例全部通过（Node v23.9.0 与 v22.14.0 均验证）：

```
ℹ tests 21
ℹ pass 21
ℹ fail 0
```

覆盖：降雨累积、渗透消退、沿坡漫流、洼地积水、历史快照查询、淹没阈值、
空间分布降雨、开放边界排水、连通区域拆分（含 4-连通对角分离）、单格/L形/
环形（含孔洞）边界多边形、面积一致性、道路栅格化、断点分段与合并。
