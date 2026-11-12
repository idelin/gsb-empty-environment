# Drag trajectory remapper

供 TypeScript 桌面操作宿主调用的拖拽轨迹重映射包。它解决 RPA 录制轨迹在窗口缩放、DPI 变化、旋转后落到错误目标的问题：

1. 用录制窗口到当前窗口的仿射矩阵映射整条录制轨迹；
2. 以映射后的首尾点为起终点，在“起终点 + 障碍顶点”可见图上规划；
3. 先最小化折点数（共线续行不算新折点），再最小化欧氏长度，最后按中间顶点 ID 的字典序打破平局；
4. 起点和终点必须在目标可拖区域内；区域边界和障碍边界可接触，但路径任一段不得有正长度部分位于障碍内部；
5. 执行器保证按下、释放各只发生一次；移动异常或取消时一定释放已按下的鼠标。

## 快速使用

```ts
import {
  remapTrajectory,
  executeDrag,
  createRecordingInjector,
} from "./index.ts";

const result = remapTrajectory({
  // 录制坐标，任一点可为 [x, y] 或 { x, y }
  trajectory: [[10, 10], [30, 12], [60, 20]],
  // DOMMatrix 风格：x' = a*x + c*y + e, y' = b*x + d*y + f
  transform: { a: 2, b: 0, c: 0, d: 2, e: 100, f: 50 },
  region: { x: 0, y: 0, width: 1920, height: 1080 },
  obstacles: [
    [
      { x: 300, y: 300, id: "top-left" },
      { x: 600, y: 300, id: "top-right" },
      { x: 600, y: 500, id: "bottom-right" },
      { x: 300, y: 500, id: "bottom-left" },
    ],
  ],
});

// result.trajectory / result.mappedTrajectory：映射后的录制点
// result.path / result.plannedPath：无碰撞规划路径
// result.bends / result.length / result.vertexIds：规划代价和顶点 ID

const recorder = createRecordingInjector();
const execution = await executeDrag(result.path, recorder);
// recorder.events: move, mousedown, move..., mouseup
```

## 输入约定

- `trajectory`、`points`、`recordedTrajectory` 均可传录制点数组。
- `transform` 支持：
  - `[a, b, c, d, e, f]`；
  - `{ a, b, c, d, e, f }`（DOMMatrix 兼容）；
  - `[[a, c, e], [b, d, f]]` 或 3x3 仿射矩阵；
  - `{ scale, rotationDeg, translate }` 便捷形式；
  - `recordedTransform` + `currentTransform`（计算 `current * inverse(recorded)`）。
- `region` 支持矩形 `{ x, y, width, height }`、`{ minX, minY, maxX, maxY }`、`[x, y, width, height]` 或任意多边形。
- `obstacles` 支持单个多边形、多边形数组，或 `{ polygons: [...] }` 包装。
- 顶点可带 `id`；没有 `id` 时按障碍输入顺序分配稳定的从 0 开始的数字 ID。

## 规划与边界

- 可见图节点为起点、终点和全部障碍顶点；两个节点之间只在整段线段不进入任何障碍内部时连边。
- 若线段沿障碍边或只接触顶点，该段合法。
- 折点按几何方向变化计数，共线中间顶点不会额外增加折点。
- 平局先比较中间顶点 ID 序列（起终点固定），因此直连路径的空序列优先于共线绕行；再以精确长度兜底。
- 起点/终点在障碍内部、在区域外、或不存在可见图路径时，抛出带 `code` 的 `TrajectoryRemapError`。可使用 `remapTrajectorySafe()` 获取 `{ ok: false, error, code }`。

## 注入器

执行器按以下方法名识别注入器（支持对象参数、`(x, y)` 参数或无参形式）：

- 移动：`moveTo` / `move` / `mouseMove` / `pointerMove`
- 按下：`mouseDown` / `down` / `pointerDown` / `press`
- 释放：`mouseUp` / `up` / `pointerUp` / `release`

`executeDrag` 为异步执行器，会等待返回 Promise 的注入方法；`executeDragSync` 用于纯同步注入器。传入 `AbortSignal` 可取消。默认取消会抛出 `DragCanceledError`，但取消发生在按下之后时，会先且仅先调用一次释放；`throwOnCancel: false` 可改为返回 `{ canceled: true, mousedownCount, mouseupCount, events }`。

## 测试

```sh
node --experimental-strip-types --test test/*.test.ts
```

测试覆盖旋转、DPI 缩放、仿射组合、可见图路径参考、对称路径的顶点 ID 平局、障碍贴边、区域/内部校验，以及正常、异常、取消三种注入序列。
