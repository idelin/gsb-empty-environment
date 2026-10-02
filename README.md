# aries — WAL 事务日志与 ARIES 恢复器（Go 标准库）

单机事务持久化与恢复管理器。每个写操作**先记日志、后改内存**（WAL 协议），
维护事务表与脏页表（DPT）；崩溃重启后按 **Analysis → Redo → Undo** 三阶段
完成恢复：已提交但未刷盘的脏页全部重做，未完成事务反向扫描日志逐条回滚。

## 结构

| 文件 | 职责 |
|---|---|
| `wal.go` | 预写日志：记录帧 `[len][crc32][payload]`，每条 fsync；打开时截断撕裂尾部 |
| `page.go` | 定长页（256B，页头 8B 存 pageLSN）、缓冲池、刷盘 |
| `system.go` | 事务管理：`Begin/Write/Commit/Abort/Checkpoint/Crash`；WAL 规则与 DPT/事务表维护 |
| `recovery.go` | ARIES 三阶段恢复 |
| `recovery_test.go` | `go test` 验收测试 |
| `cmd/demo` | 运行样例：批量转账中断电 → 重启恢复 |

## 关键设计

- **WAL 协议**：`Write` 先 `Append` UPDATE 日志（含前/后像，fsync 落盘）再改内存页；
  `Commit` 在 COMMIT 记录 fsync 后才返回，因此已提交事务必然可重做。
- **pageLSN 幂等重做**：页头记录最后应用的 LSN，Redo 只应用 `pageLSN < record.LSN`
  的记录，恢复可重复执行（恢复过程中再次崩溃也安全）。
- **CLR（补偿日志）**：Undo 每回滚一条 UPDATE 就写一条 CLR（含 `undoNextLSN`），
  恢复过程中再次崩溃不会重复回滚。
- **检查点**：记录事务表 + DPT 快照并刷全部脏页，Analysis 从最近的检查点开始。
- **断电模拟**：`Crash()` 直接关闭文件句柄并丢弃缓冲池与内存表——日志已逐条
  fsync 所以幸存，脏页全部丢失，等价于突然断电。

## 恢复三阶段

1. **Analysis**：从最近检查点扫描到日志尾，重建事务表（Running/Committed/Ended）
   与 DPT（pageID → recLSN）。
2. **Redo**：从 DPT 最小 recLSN 开始重放所有 UPDATE/CLR（含失败事务的），
   依据 DPT 与 pageLSN 跳过已在盘上的记录，把数据库精确还原到崩溃时刻；
   随后为已提交但缺 END 的事务补写 END。
3. **Undo**：对事务表中剩余的 loser 事务，按全局 LSN 逆序沿 `prevLSN` 链回滚，
   每条写 CLR，链走到头写 END；最后刷盘。

## 运行

```sh
go test ./... -v     # 验收测试
go run ./cmd/demo    # 断电恢复演示（生成 ./demo_data）
```

## 测试结果

```
=== RUN   TestCrashDuringBatchUpdate      --- PASS  # 验收场景：批量更新中断电
=== RUN   TestWinnerCommittedButNotEnded  --- PASS  # COMMIT 与 END 之间崩溃
=== RUN   TestCheckpointThenCrash         --- PASS  # 检查点后崩溃
=== RUN   TestRecoveryIsIdempotent        --- PASS  # 恢复幂等（连续恢复 3 次）
=== RUN   TestMultipleLosersInterleaved   --- PASS  # 多 loser 交错，全局逆序回滚
=== RUN   TestAbortRollsBack              --- PASS  # 正常运行期 Abort 写 CLR
=== RUN   TestTornLogTail                 --- PASS  # 日志尾部撕裂写被截断
=== RUN   TestWALProtocolLogBeforeData    --- PASS  # 先日志后数据，DPT 记录 recLSN
PASS
ok  	aries
```

## 样例输出（`go run ./cmd/demo`）

```
initialized:   acct0=1000  acct1=1000  acct2=1000  acct3=1000  acct4=1000  | total=5000

running batch of transfers (committing, but never flushing pages)...
  transfer 00 committed: acct0 -50 -> acct2 +50
  ...
  transfer 11 committed: acct1 -50 -> acct3 +50

*** transfer 12 in flight: debited acct0 by 200, then POWER LOSS ***
*** (credit to acct3 never happened, txn 14 never committed) ***

restarting, running ARIES recovery...
  analysis: checkpoint LSN=8, redo starts at LSN=1
  redo:     re-applied 25 log records (committed, not yet on disk)
  undo:     loser txns=[14], rolled back 1 updates via CLRs

recovered:    acct0=950  acct1=950  acct2=1050  acct3=1050  acct4=1000  | total=5000
invariant OK: total funds unchanged, no committed data lost, uncommitted debit rolled back

WAL tail after recovery:
  LSN=69 BEGIN      txn=14 prev=0
  LSN=70 UPDATE     txn=14 prev=69 page=0 off=8
  LSN=71 CLR        txn=14 prev=70 page=0 off=8 undoNext=69
  LSN=72 END        txn=14 prev=69
```
