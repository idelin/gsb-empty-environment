# marker-overlay

TypeScript 标记库：把多个分析器产出的半开范围 `[start, end)` 按优先级叠加，
输出最小不交片段（segment）及每个片段的最终样式。

## 规则

- **半开范围**：`start` 含、`end` 不含；首尾相接的范围不重叠。
- **按属性键裁决**：每个属性键独立裁决——覆盖该位置且优先级最高的标记赢得该键，
  不同键可来自不同标记（叠加合并）。
- **同优先级仲裁**：优先级相同由 `sourceId` 裁决（字典序大者胜），再相同则由
  marker `id`（小者胜）保证确定性。
- **空范围即锚点**：`start === end` 的标记不占任何字符、不参与样式计算，
  通过 `anchors()` 单独获取。
- **最小不交片段**：`segments()` 先按所有边界切分，再丢弃无样式区间、
  合并最终样式相同的相邻区间。

## API

```ts
import { MarkerSet } from "./src/markers.ts";

const set = new MarkerSet();
set.add({ id: "m1", start: 0, end: 10, priority: 1, sourceId: "lint",
          attrs: { color: "red" } });
set.update("m1", { priority: 5 });   // 更新范围 / 优先级 / 属性
set.remove("m1");                     // 删除
set.segments(0, 64);                  // => [{ start, end, attrs }, ...]
set.resolveAt(4);                     // 单点最终样式
set.anchors();                        // 空范围锚点
```

## 测试

`test/model.ts` 是独立的逐字符参考模型：对 `[0, N)` 的每个字符暴力求解
最终样式再聚合成段。`test/markers.test.ts` 用它核对更新、删除、相邻合并，
并含 200 轮随机化对照（随机增删改后逐轮比对）。

需要 Node ≥ 23.6（内置 TypeScript 类型擦除）或 Node ≥ 22.6 加
`--experimental-strip-types`：

```sh
node --test "test/**/*.test.ts"
```
