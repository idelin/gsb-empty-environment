# Seam Carver

在整数能量图上寻找并删除最小能量的顶到底垂直缝线（seam carving），支持保护点与连续多轮删除。

## 规则

- 每行恰好选一个像素，相邻行横坐标之差不超过 1（`|dx| <= 1`）。
- 每轮返回该条缝线的总能量与**原图坐标**路径（连续删除时坐标自动映射回原图）。
- 保护点（`protectedPoints`，原图坐标）永远不会被删除；若某一行被完全保护等导致无可行缝线，本轮返回 `null` / 报告中 `feasible = false` 并给出原因。
- 连续删除多条时**逐轮重新选缝**：每删一条即更新剩余像素，并通过 `recomputeEnergy` 回调重算能量后再找下一条，绝不一次选多条再批量删除。
- 平手时取横坐标较小者，结果确定可复现。

## 用法

```ts
import { carveSeams, SeamCarver, gradientEnergy } from "./src/seam-carver.ts";

const energy = [
  [1, 4, 3],
  [5, 2, 1],
  [3, 3, 1],
];

const report = carveSeams(energy, 2, {
  protectedPoints: [{ x: 1, y: 1 }],
  recomputeEnergy: (remaining) => remaining.map((row) => row.map((p) => p.energy)),
});

for (const seam of report.seams) {
  console.log(seam.round, seam.energy, seam.path); // path 为原图坐标
}
if (!report.feasible) console.warn(report.reason);
```

## API

- `new SeamCarver(energy, options?)` — `energy[y][x]` 为非空矩形整数能量图，否则抛错。
- `carver.findSeam()` — 当前最小缝线（当前列坐标），无可行缝线返回 `null`。
- `carver.carveOne()` — 删除一条，返回 `{ round, energy, path }`（原图坐标）或 `null`。
- `carver.carve(count)` / `carveSeams(energy, count, options)` — 连续删除，返回 `{ seams, removed, feasible, reason }`。
- `options.protectedPoints` — 原图坐标的保护点，越界抛错。
- `options.recomputeEnergy(remaining)` — 每次删除后回调，`remaining[y]` 为剩余像素的 `{ x: 原图横坐标, y, energy }`，须返回同形状的新能量图；不提供则保留原能量值。
- `gradientEnergy(gray)` — 辅助函数，由灰度图生成 `|dx| + |dy|` 整数能量图。

## 运行

```sh
npm test        # 运行测试（含暴力枚举对照、坐标映射、逐轮重算、不可行检测）
npm run demo    # 运行示例
npm run typecheck   # 需要安装 typescript（npm install）
```

说明：本环境无法安装 TypeScript 编译器，测试通过 `scripts/strip-types.mjs`
擦除类型后在 Node 上直接执行同一份实现逻辑；正常使用请以 `tsc` 编译为准。
