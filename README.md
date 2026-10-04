# dep-task-executor

带依赖关系的异步任务执行器（TypeScript，零依赖），支持运行中撤销节点、失败/撤销传播与原因链记录。

## 运行要求

Node.js >= 23（或 >= 22.6 并加 `--experimental-strip-types`），直接运行 `.ts` 源码，无需编译。

```sh
npm test   # node --test
```

## 核心语义

- **状态机**：`pending → running → completed | failed | cancelled`，终态不可逆。
- **调度**：依赖全部 `completed` 后节点进入就绪，按 `concurrency` 限制并发启动。
- **撤销 `cancel(node, message?)`**：
  - 目标是 `pending` / `running`：立即置为 `cancelled`（同步生效），运行中的任务收到 `AbortSignal`；
  - 目标是终态：返回 `false`，不做任何事。
- **传播规则**：
  - 依赖被**撤销** → 未启动的后继**立即**级联撤销（不动点循环内同步完成）；
  - 依赖**失败** → 等该节点**全部**依赖落定后再撤销，以便收集同时失败的全部主因；
  - 已启动（`running`）或已完成的节点不受波及，保留自身结果。
- **主因选择**：多个依赖同时失败/撤销时，`sources` 记录全部肇事依赖（升序），
  `primarySource` 取**节点编号最小**者，`cause` 链接到主因节点的失败/撤销记录，形成完整原因链。
- **完成与撤销竞争**：`cancel()` 同步生效。撤销先于 promise 反应被处理时，迟到的结果被丢弃
  （撤销赢）；任务已完成后再撤销则是 no-op（完成赢）。

## API 摘要

```ts
const ex = new Executor({ concurrency: 4 });
const a = ex.add('a', async ({ signal }) => 1);      // 返回节点编号
ex.add('b', taskB, [a, 'a']);                        // 重复边自动去重
ex.dependsOn('c', 'a', 'b');                         // 环检测，抛错
await ex.run();                                      // 全部节点进入终态后解决
ex.cancel('b', 'reason');                            // 运行中撤销
ex.record('c');                                      // { state, cancellation, failure, result }
ex.result<number>('a');                              // 未完成则抛错
```

`CancelRecord` 结构：

```ts
{
  node: 2,                    // 被撤销的节点编号
  kind: 'dependency',         // 或 'explicit'
  message: '...',
  sources: [0, 1],            // 全部失败/撤销的依赖，按编号升序
  primarySource: 0,           // 主因 = 最小编号
  cause: { node: 0, ... },    // 主因节点的记录，递归构成原因链
}
```

## 测试覆盖（test/executor.test.ts）

- 依赖顺序执行与结果读取
- 重复边去重（`add` 的 deps 与 `dependsOn` 均幂等）
- 菱形依赖：汇合节点恰好执行一次
- 运行中撤销：未启动后继级联撤销，已启动节点保留结果
- 原因链：跨多级传播逐节链接
- 多依赖同时失败：按节点编号选主因
- 失败等待全部依赖落定 / 撤销立即传播
- 完成与撤销双向竞争
- AbortSignal 通知、终态撤销 no-op、并发上限、环检测、启动后冻结图
