# ARIES 崩溃恢复引擎(Go 标准库实现)

数据库经典 **ARIES**(Algorithm for Recovery and Isolation Exploiting Semantics)
预写日志(WAL)与崩溃恢复协议的最小完整实现,含异常注入容错验证。
仅依赖 Go 标准库。

## 架构

```
wal/        预写日志:记录类型、CRC32 帧编码、缓冲式 WAL、故障注入、日志传输
storage/    页面层:持久页存储("磁盘")+ 易失缓冲池,Crash() 模拟断电
recovery/   ARIES 三阶段恢复引擎 + 事务管理器(Begin/Update/Commit/Abort/Checkpoint)
cmd/demo/   端到端运行样例
```

### ARIES 三阶段

1. **Analysis**(`recovery/recovery.go`):从最近的 Checkpoint 起正向扫描,
   重建 **TransactionTable**(事务状态 + lastLSN)与 **DirtyPageTable**
   (pageID → recLSN),区分 winner(已提交)与 loser(未提交)事务。
2. **Redo**(历史重复):从 DPT 最小 recLSN 起扫描,重放所有 UPDATE/CLR。
   仅当页面在 DPT 中且 `recLSN ≤ rec.LSN` 且 `pageLSN < rec.LSN` 时才应用,
   保证幂等(恢复可重复执行)。
3. **Undo**(反向扫描):按 LSN 降序(跨事务归并)回滚 loser 事务,
   每个被撤销的更新写入 **CLR 补偿日志**(`UndoNextLSN` 指向下一待撤销记录),
   使"恢复过程中再次崩溃"也能安全续撤,绝不重复撤销。

### 日志记录

`BEGIN / UPDATE / COMMIT / ABORT / END / CLR / CHECKPOINT`,二进制帧格式
`[uint32 长度][记录体][CRC32]`。扫描遇损坏/截断帧即停——这正是崩溃后
磁盘尾部的真实形态;`WAL.Open` 会将撕裂尾部截断实现**自愈**。

## 异常注入(恶劣工况)

`wal.Injector` 以并发安全的方式包裹底层 writer:

| 故障 | 行为 | 恢复语义 |
|---|---|---|
| `FaultPowerLoss` 断电 | 写"成功"但数据不落盘 | 未落盘记录(含已 ack 的 commit)崩溃后无痕迹 |
| `FaultDiskFull` 磁盘满 | 写返回 ENOSPC | commit 原子回滚出日志缓冲;空间释放后重试即自愈 |
| `FaultTornWrite` 撕裂写 | 只写一半即失败 | 重开时截断损坏尾部,日志继续可用 |
| `LossyLink` 网络丢包 | 按百分比丢帧 | 日志传输层重传 + 副本端按 LSN 幂等去重 |

关键设计:`WAL.AppendAndFlush` 把 COMMIT 记录的"追加+落盘"做成原子操作,
磁盘满时失败的 commit 会被回滚出日志缓冲——**失败的提交永远不会变得持久**。

## 运行

```bash
go run ./cmd/demo     # 端到端崩溃恢复演示
go test ./...         # 全部测试
go test -race ./...   # 并发竞态检测
```

## 运行样例输出

```
=== Phase 0: run transactions ===
txn1: UPDATE page1 <- AAAA, COMMIT
checkpoint written at LSN 5 (buffer pool + log flushed)
txn2: UPDATE page2 <- BBBB, COMMIT (dirty page only in buffer pool)
txn3: UPDATE page3 <- CCCC, never commits
txn4: UPDATE page4 <- DDDD, never commits
txn5: UPDATE page5 <- EEEE, never commits

=== CRASH: sudden power loss (buffer pool vaporized) ===

=== Phase 1: Analysis ===
checkpoint at LSN 5; dirty page table: map[1:2 2:7 3:11 4:13 5:15]
loser transactions (uncommitted at crash): [3 4 5]

=== Phase 2: Redo (repeat history) ===
re-applied 4 records, skipped 1 already-durable records

=== Phase 3: Undo (rollback losers with CLRs) ===
wrote 3 compensation log records, 3 END records

=== Final state ===
page1: "AAAA"
page2: "BBBB"
page3: "\x00\x00\x00\x00"
page4: "\x00\x00\x00\x00"
page5: "\x00\x00\x00\x00"
```

## 测试结果

`go test -race -count=1 ./...` 全部通过(16 个测试):

```
ok  aries/recovery    — TestRecoverCheckpointThenCrash   验收场景:2 提交 + 3 未提交,Checkpoint 后崩溃
                      — TestCrashDuringUndo              Undo 途中再崩溃,沿 UndoNextLSN 续撤
                      — TestDiskFullCommitFailsThenHeals 磁盘满 commit 失败 → 回滚 → 自愈
                      — TestPowerLossDropsUncommittedTail 断电丢失未落盘尾部
                      — TestLogShippingReplicaRecovery   40% 丢包下日志复制,主备恢复一致
                      — TestConcurrentTransactions       8 goroutine × 20 事务混合提交/中止后恢复
                      — TestConcurrentWALAppend          并发 LSN 分配唯一无间隙
ok  aries/wal         — 编解码往返 / 损坏帧拒绝 / 撕裂写截断自愈 / 磁盘满重试
                      / 断电丢尾 / 失败 commit 原子回滚 / 丢包重传幂等
```

验收边界(`TestRecoverCheckpointThenCrash`)严格断言:

- Analysis 找到 Checkpoint,精确识别 3 个 loser 事务,DPT 覆盖全部脏页;
- Redo 重放 4 条记录(1 条已提交 + 3 条未提交更新),已持久化记录被跳过;
- Undo 恰好写 3 条 CLR;已提交数据(页 1、2)持久化,未提交修改(页 3、4、5)全部回滚;
- 恢复后事务表为空;二次恢复为无操作(幂等);
- 并发测试断言无 goroutine 泄漏(`runtime.NumGoroutine` 前后一致)。
