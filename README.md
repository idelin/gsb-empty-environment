# twopc — 可回退两阶段提交协调器

纯 Go 标准库实现的 2PC 协调器，面向离散事件（同步消息 + 超时）环境。

## 设计

- **WAL 预写式状态转移**（`wal.go`）：每次状态转移（`PREPARING → PREPARED →
  COMMITTING → COMMITTED` / `ABORTING → ABORTED`）先追加 JSON 行记录并
  `fsync`，再发送对应消息。重放时容忍崩溃造成的撕裂尾行。
- **协调器**（`coordinator.go`）：
  - 准备阶段并发收集投票，逐票落盘；重复投票按参与者去重。
  - 任一 NO 或超时未决 → 全局 ABORT；全票 YES → 记录 `PREPARED` 后决议
    COMMIT，记录 `COMMITTING` 后再投递。
  - 所有参与者调用带 `context` 超时与有界重试；超过 `MaxRetries` 仍未送达
    的参与者写入 `AUDIT` 未决审计事件，事务通过 `Pending()` 暴露。
- **崩溃恢复**（`Coordinator.Recover`）：重放 WAL 重建事务表，按状态决议：
  - `PREPARING`：不可能有任何提交，安全 ABORT，释放参与者锁（防孤儿）。
  - `PREPARED`：向在线参与者 `Query`；若任一方已获知决议则跟随，否则
    全票 YES 保证提交安全，补齐 COMMIT。
  - `COMMITTING` / `ABORTING`：幂等重投决议，直到全部确认或记入审计。
- **参与者**（`participant.go`）：`MemoryParticipant` 支持故障注入
  （`SetDown`/`SetDelay`/`SetVoteFunc`），所有操作幂等，重复 Prepare 返回
  原投票。

## 运行

```sh
go test ./... -v   # 测试（含两条验收边界）
go run ./cmd/demo  # 四个场景的运行样例
```

## 验收边界对应测试

- `TestRecoverCompletesCommitAfterCrash`：协调者在发布 Commit 前崩溃，重启后
  重放 WAL、查询在线参与者并完成提交。
- `TestPendingAuditAfterRetryLimit`：参与者无响应达到重试上限后记录未决审计
  事件；参与者恢复后由恢复流程完成投递。
