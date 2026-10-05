# publish-queue

文件发布队列库（TypeScript，零运行时依赖）。

## 语义

- **提交请求**：`submit({ path, expectedVersion, digest, operationId? })`。
  `expectedVersion` 为预期旧版本（`null` 表示目标必须不存在），`digest` 为新内容摘要，
  `operationId` 为幂等键（缺省自动生成）。
- **同路径串行**：同一 `path` 的操作挂到该路径的执行链尾部依次执行；
  不同路径各自成链，互不阻塞、并行推进。
- **版本竞争**：写入经 `VersionStore.compareAndSwap` 乐观校验，
  期望版本与当前版本不符时返回 `conflict` 及当前版本号，不产生副作用。
- **取消**：确认点（执行器提交成功）之前 `cancel()` 有效——排队中直接置为
  `cancelled`，执行中通过 `AbortSignal` 中断投递；确认之后 `cancel()` 返回
  `'committed'`，不会宣称撤销。
- **幂等恢复**：每个操作的终态写入 `Journal`；`recover(operationId)` 或以相同
  `operationId` 重新 `submit` 时直接返回已记录结果，不重新执行。
  确认回执丢失（提交生效但响应未送达）时，按幂等键向存储对账，找回已提交回执。

## 执行器与故障注入

- `PublishExecutor`：投递接口，`deliver(command, signal)` 返回确认回执。
- `StoreBackedExecutor`：基于 `VersionStore` 的可用实现。
- `ScriptedExecutor`：测试包装器，按 `operationId` / `path` 注入
  `delayMs`（可被取消信号中断）与失败：
  - `before-commit`：确认点前失败，存储无副作用；
  - `receipt-loss`：提交生效但确认回执丢失。

## 结构

- `src/types.ts` — 请求 / 回执 / 终态 / 句柄类型
- `src/store.ts` — 版本存储（CAS）与内存实现
- `src/journal.ts` — 终态日志（幂等恢复依据）
- `src/executor.ts` — 执行器接口、存储实现、故障注入实现
- `src/queue.ts` — 队列：串行链、取消、对账恢复
- `test/publish-queue.test.ts` — 场景验证

## 运行测试

需要 Node ≥ 22.6（`--experimental-strip-types`）或 ≥ 23.6（原生支持 TypeScript）：

```sh
npm test
```

测试覆盖：同路径版本竞争（链式推进 + 过期期望冲突）、跨路径独立推进
（慢路径不阻塞快路径）、确认回执丢失（对账恢复 + 重启后幂等重放）、
取消语义（排队中 / 执行中 / 确认后）、失败注入（无副作用且链不中断）。

如需类型检查：`npm i -D typescript @types/node && npm run typecheck`。
