# inbox：本地事务收件箱 + 稳定输出 ID

业务事件按**至少一次**投递，本库保证**业务效果恰好一次**、**网络发送至少一次且输出 ID 稳定**。

## 语义划分

| 阶段 | 保证 | 机制 |
| --- | --- | --- |
| 事件处理 | 业务效果恰好一次 | 去重记录 + 状态修改 + 输出消息在**同一个本地事务**提交 |
| 输出投递 | 网络发送至少一次 | 发送成功但回执（`MarkSent`）丢失 → 重启后重发 |
| 接收方 | 效果恰好一次 | 输出 ID 稳定（`eventID#seq`），接收方按 ID 去重 |

关键不变式：

- `Processor.Process` 里 `Seen → Record → handler(改状态) → Enqueue → Commit`
  是一个原子单元；commit 失败则什么都不生效，可安全重试（`inbox.go`）。
- 输出 ID 由事件 ID 确定性导出（`StableOutputID`），重发不产生新 ID。
- `Dispatcher` 在 `Send` 与 `MarkSent` 之间崩溃时，恢复后重发**同一条**输出。

## 两个独立持久化模型

同一套崩溃恢复语义，用两种互不相干的持久化机制验证：

- `wal/`：追加式日志。每个事务是**一条**带 CRC32 的记录（单次 write + fsync），
  启动时重放并截断崩溃留下的撕裂尾部。
- `snapshot/`：整态快照。每次提交写临时文件、fsync、原子 `rename` 替换，
  崩溃只会留下旧快照或新快照，残留 `.tmp` 被忽略。

## 验证（`inbox_test.go`，对两个模型各跑一遍）

1. `TestDuplicateDelivery` — 重复投递只生效一次，输出仅一条且 ID 稳定。
2. `TestCommitFailureThenRetry` — commit 前崩溃不残留任何效果，重试后恰好一次。
3. `TestCrashAfterCommitBeforeDispatch` — commit 后、发送前崩溃，重启后输出补发。
4. `TestAckLostResendWithStableID` — 发送后回执丢失 → 重发同 ID；
   网络发送 2 次，接收方只应用 1 次。
5. `TestRedeliveryStorm` — 20 个事件 × 5 次重复投递，效果与输出各 20 个。
6. `TestHandlerErrorRollsBack` — handler 失败整体回滚，可重试。

另有模型专属测试：`wal/wal_test.go`（撕裂尾部截断）、
`snapshot/snapshot_test.go`（残留临时文件忽略）。

## 运行

```sh
go test ./...
go run ./cmd/demo   # 端到端演示：重复投递 → 崩溃 → 丢回执 → 稳定 ID 重发
```
