# keyed-executor

按键串行、跨键共享有限并发额度的 TypeScript 作业执行器。

## 语义

- **同键串行**：相同 `key` 的作业严格按提交顺序逐个执行。
- **跨键共享额度**：不同 `key` 的作业共享 `maxConcurrency` 全局并发额度；
  就绪的 key 进入 FIFO 就绪队列轮转调度，调度顺序确定。
- **失败不阻塞**：作业抛异常或返回 rejected Promise 不会阻塞同键后继，
  也不会占住并发额度。
- **可取消**：`handle.cancel()` 对排队中的作业生效（Promise 以 `CancelledError` 拒绝）；
  运行中或已结束的作业取消返回 `false`。
- **空闲释放**：某个 key 既无运行中也无排队作业时，其内部状态同步释放
  （`keyCount` 可观察），后续同键提交会重建状态。
- **关闭收敛**：`close()` 停止接收新作业并等待存量排空；关闭期间的提交
  （包括运行中作业的递归提交）以 `ExecutorClosedError` 拒绝且不计入待处理数，
  因此关闭流程必然收敛。

## 关键边界场景

- **回调同步提交同键任务**：`settle` 在一个同步临界区内完成状态翻转、
  空闲 key 清理与重新调度，用户 `then` 回调（微任务）运行时看到的必是干净状态；
  运行中作业同步 `submit` 同键任务只会入队，等待当前作业结束。
- **清理时新任务进入**：清理与 Map 删除是同步原子的，新提交不会挂在
  待删除的僵尸状态上，也不会与旧状态双重注册。
- **关闭期间递归提交**：关闭后 `submit` 立即返回被拒绝的句柄，不进入队列、
  不影响 `pendingCount`，递归提交链确定性终止，`close()` 正常 resolve。

## API

```ts
const executor = new KeyedExecutor({ maxConcurrency: 4 });

const handle = executor.submit('user-42', async () => fetchProfile());
handle.cancel();            // 仅排队中有效
await handle.promise;       // 结果或 CancelledError / ExecutorClosedError / 作业异常
handle.state;               // queued | running | completed | failed | cancelled | rejected

await executor.close();     // 停止接收新作业，排空后 resolve
```

诊断属性：`runningCount` / `pendingCount` / `keyCount` / `isClosed`。

## 运行测试

需要 Node ≥ 22.6（依赖原生 type stripping；无需任何 npm 依赖）：

```sh
npm test
```

本机可用 `nvm use v23.9.0` 或
`~/.nvm/versions/node/v23.9.0/bin/npm test`。

测试基于 `node:test`，通过手动闸门（deferred gate）+ 事件日志断言
确定性的调度顺序，覆盖上述全部边界场景。
