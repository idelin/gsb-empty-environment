# reactive-loop-guard

带周期检测的响应式循环更新守护器（TypeScript，零依赖）。

在 `Signal` / `Computed` / `Effect` 响应式模型之上提供死锁保护：

- **迭代阈值阻断**：同一传播纪元（flush epoch）内，响应式节点的执行次数达到
  `maxIterations`（默认 50）时，抛出 `CircularSignalUpdate`，强制阻断死循环。
- **调用栈环路检测**：节点在执行栈上被重入（如 `Computed` 互相求值成环）时立即抛出
  `CircularSignalUpdate`。
- **循环因果链报告**：异常携带 `report`（触发源、迭代次数、环路径、因果事件链），
  `error.message` 为格式化好的可读报告。

## 环境要求

Node.js >= 23.6（原生运行 TypeScript）。Node 22.6+ 可加
`--experimental-strip-types` 运行。

## 使用

```ts
import { CircularSignalUpdate, createReactiveSystem } from './src/index.ts';

const sys = createReactiveSystem({ maxIterations: 50 });
const A = new sys.Signal(0, 'A');

try {
  // 恶意逻辑：读取 A 又写回 A，直接写自身依赖
  new sys.Effect(() => {
    A.value = A.value + 1;
  }, 'effect-A');
} catch (err) {
  if (err instanceof CircularSignalUpdate) {
    console.error(err.message); // 详细循环因果链报告
  }
}
```

报告示例：

```text
CircularSignalUpdate: reactive loop blocked (reason: iteration-threshold)
iterations: 50 (threshold: 50)
trigger source: effect-A
cycle: effect-A -> A -> effect-A
causal chain (last 8 of 98 events):
  effect-A --writes--> A
  A --notifies--> effect-A
  ...
```

## 运行

```sh
npm test     # node --test "test/**/*.test.ts"
npm run demo # node examples/demo.ts
```

## 结构

- `src/guard.ts` — `ReactiveLoopGuard`（迭代计数、执行栈、因果链记录）与
  `CircularSignalUpdate` 异常 / `LoopReport` 报告。
- `src/reactive.ts` — `createReactiveSystem()` 工厂，产出相互隔离的
  `Signal` / `Computed` / `Effect` 与各自独立的 guard。
- `test/guard.test.ts` — Node 内置测试（`node:test` + `node:assert`）。
- `examples/demo.ts` — 正常运行、Effect 自写依赖、Computed 成环三个场景的演示。
