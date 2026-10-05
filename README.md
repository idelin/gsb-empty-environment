# trace-diff 轨迹差分工具

比较两个执行器的轨迹，允许两侧含有不同数量的**无副作用内部步骤**；
按输入给定的**可观察动作定义**对齐轨迹，定位**首次语义分歧**并还原
分歧点附近的**内部窗口**。支持**循环压缩段**（惰性展开，绝不整体展开）。

## 轨迹模型

```ts
type TraceEvent = StepEvent | LoopSegment;

interface StepEvent { kind: 'step'; action: string; value?: unknown; meta?: ... }
interface LoopSegment { kind: 'loop'; repeat: number; body: TraceEvent[] } // 可嵌套
```

可观察动作由输入定义，支持精确名与前缀通配：

```ts
const spec = { actions: ['http.request', 'db.*'] };
```

未匹配的动作视为无副作用的内部步骤，对齐时跳过，但会记入分歧窗口。

## 用法

```ts
import { diffTraces } from './src';

const report = diffTraces(traceA, traceB, spec, {
  windowSize: 5,       // 分歧点前后各保留的内部步骤数
  lookaheadLimit: 256, // 乱序/缺失判定的前瞻上限（可观察动作数）
});
```

## 分歧类型

- `value-mismatch`：同一可观察动作的值不同（深比较）。
- `missing-action`：一侧缺失某可观察动作（含一侧轨迹先耗尽、循环次数不同）。
- `out-of-order`：动作都存在但顺序不同。通过在另一侧做**有界前瞻**与缺失区分：
  当前动作在另一侧未来出现 → 乱序；否则 → 缺失。前瞻达到上限时结论会标注
  `truncated`。

## 关键设计

- **惰性展开**：`TraceCursor` 用显式栈逐层推进循环段，`repeat` 为 1e9 也不会
  整体展开；`clone()` 仅复制栈帧，供前瞻使用而不消耗主游标。
- **内部窗口还原**：主游标途经的内部步骤进入环形缓冲（分歧点之前），分歧后
  继续采集紧随的内部步骤（分歧点之后），两侧分别输出。
- **位置路径**：每个可观察动作附带可读路径，如 `A[1].loop(第3轮)[1]`，
  精确反映嵌套循环中的位置。

## 输出结构

```ts
interface DiffReport {
  equal: boolean;
  divergence?: { kind; observableIndex; a?; b?; lookahead?; message };
  window: { a: InternalWindow; b: InternalWindow }; // before/after 内部步骤
  observedCounts: { a: number; b: number };
}
```

## 构建与测试

```sh
tsc -p tsconfig.json && node dist/test/run-tests.js
```
