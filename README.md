# file-publish-queue

文件发布队列库（TypeScript，零依赖，Node ≥ 22.6 直接运行）。

## 语义

- **提交请求**：`publish({ path, expectedVersion, digest, idempotencyKey? })`，返回 `PublishHandle`（`done: Promise<PublishOutcome>` + `cancel()`）。
- **同路径串行**：同一路径的请求严格按提交顺序执行；**不同路径并行**，互不阻塞。
- **乐观并发**：提交前校验 `expectedVersion`，不匹配则以 `conflict` 结束（携带当前版本）。
- **确认点与取消**：确认点是原子 compare-and-swap 提交。确认点前 `cancel()` 有效（`{ ok: true, status: 'cancelled' }`）；进入提交后返回 `{ ok: false, status: 'committing' }`，已提交后返回 `{ ok: false, status: 'committed', receipt }` —— 确认后绝不宣称撤销。
- **回执丢失**：提交已落库但确认回执丢失（`ReceiptLostError`）时，队列读取存储对账，若版本与摘要符合预期则恢复为 `committed`。
- **幂等恢复**：已提交结果先写 `Journal` 再通知调用方。重启后 `recover()` 重放日志，相同 `idempotencyKey` 的重提交直接返回已记录回执，不再执行；崩溃窗口（落库但日志未写）通过存储状态识别，幂等返回已提交。

## 可注入的执行器

`Executor` 包装 `FileStore`，支持按请求注入延迟与失败：

```ts
new Executor(store, {
  delays: { check: 10, beforeCommit: (req) => req.path === '/slow' ? 80 : 0, receipt: 0 },
  faults: {
    check: (req) => false,        // 校验失败
    beforeCommit: (req) => false, // 提交前失败（未落库）
    receipt: (req) => true,       // 落库但丢失确认回执
  },
});
```

## 运行测试

```sh
node --test 'test/*.test.ts'   # Node ≥ 22.6（内置类型擦除）
```

覆盖：同路径版本竞争、跨路径独立推进、确认前/后取消、确认回执丢失、日志恢复重放、崩溃窗口幂等、注入失败后续推进。
