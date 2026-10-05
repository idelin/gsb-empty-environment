# async-task-scope

异步任务作用域（结构化并发）：父任务派生子任务；任一任务失败时请求取消整棵子树，
但作用域会等待所有后代的清理结束（包括关闭过程中继续派生的后代）才收尾。

## 语义

- `runTaskScope(body)` / `TaskScope.run(body)`：在根作用域中运行 `body`，等待整棵任务树结束。
- `scope.spawn(childBody)`：派生子任务，返回 `{ seq, result }`；`seq` 为创建序号（树内单调递增）。
- `scope.onCleanup(fn)`：注册清理函数，在主体结束且子任务排空后按 LIFO 执行。
- `scope.signal` / `scope.throwIfCancelled()`：取消请求通过 `AbortSignal` 传播，任务自行响应。
- 关闭过程中仍允许 `spawn`（新任务立即拿到已中止的 signal），这些后代同样会被等待。
- 失败汇总为 `TaskScopeError`：
  - `failures` 按创建序号稳定排序，包含原始错误（`phase: 'run'`）与清理错误（`phase: 'cleanup'`）。
  - 因关闭而抛出的取消错误（`CancelledError` / `AbortError`）不计入失败。
  - `cause` / `rootCause` 永远是按序首个非取消错误，取消错误不会覆盖根因。

## 运行测试

需要 Node.js ≥ 23.6（或 ≥ 22.6 并加 `--experimental-strip-types`）以原生运行 TypeScript：

```sh
node --test test/task-scope.test.ts
```
