# aries — WAL 事务日志与 ARIES 恢复器（Go 标准库实现）

单机事务持久化与恢复管理器：**先记日志后改内存（WAL）**，维护**事务表**与
**脏页表（DPT）**，崩溃重启后按 ARIES 的 **Analysis → Redo → Undo** 三阶段
完全恢复。仅使用 Go 标准库。

## 设计

| 文件 | 职责 |
|---|---|
| `log.go` | 日志记录（UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT）的编码、CRC 帧、追加、刷盘、顺序扫描与按 LSN 随机访问 |
| `disk.go` | 定长页数据文件的读写 |
| `buffer.go` | 缓冲池；页头 8 字节持久化 pageLSN；脏页只在检查点/正常关闭时写回 |
| `txn.go` | 事务：Write 先追加 UPDATE 日志再改内存页；Commit 强制刷日志；Abort 走 CLR 回滚 |
| `db.go` | DB 打开/关闭、检查点、`Crash()` 断电模拟（WAL 落盘、脏页丢弃） |
| `recovery.go` | ARIES 三阶段恢复 |

关键机制：

- **WAL 协议**：`Txn.Write` 先 `Append` UPDATE 记录（含 before/after 镜像），
  再修改内存页并置 pageLSN；`Commit` 把提交记录 fsync 后才返回。
- **检查点**：写入含事务表与 DPT 快照的 CHECKPOINT 记录，随后刷全部脏页。
- **Analysis**：从最近的检查点出发正向扫描，重建事务表与 DPT。
- **Redo**：从 DPT 最小 recLSN 起重复历史；仅当页在 DPT 中且
  `pageLSN < record.LSN` 时才重放（幂等）。
- **Undo**：对失败者事务沿 prevLSN 链反向扫描，每个被撤销的 UPDATE 写一条
  CLR（含 undoNextLSN，崩溃重恢复时不会重复撤销），链尽后写 END。
- **崩溃模拟**：`Crash()` 只保证 WAL 落盘，直接丢弃缓冲池中的脏页；
  日志尾部的撕裂写由长度+CRC 校验截断。

## 运行样例

```console
$ go run ./cmd/demo
=== Phase 1: seed accounts, commit, checkpoint (flushed to disk) ===
seeded:    page0="alice=100" page1="bob=100"

=== Phase 2: batch of transfers ===
txn 2 committed (alice-20, bob+20); dirty pages still only in memory
txn 3 in-flight (alice-20, bob+20); never commits
in-memory: page0="alice=60 " page1="bob=140"

*** POWER LOSS: dirty pages in memory are gone, WAL survives ***

=== Phase 3: restart, ARIES recovery (analysis -> redo -> undo) ===
analysis: txn table=map[3:RUNNING] DPT=map[0:1 1:2] redo-from LSN=1
redo:     replayed LSNs [6 7 10 11]
undo:     loser txns [3] rolled back with CLRs at LSNs [12 13]
recovered: page0="alice=80 " page1="bob=120"

OK: committed txn 2 durable, uncommitted txn 3 rolled back, data intact.
```

## 测试结果

```console
$ go test -v ./...
=== RUN   TestCrashRecovery
--- PASS: TestCrashRecovery (0.00s)
=== RUN   TestAbortRollsBack
--- PASS: TestAbortRollsBack (0.05s)
=== RUN   TestCleanShutdownReopen
--- PASS: TestCleanShutdownReopen (0.00s)
=== RUN   TestRepeatedRecovery
--- PASS: TestRepeatedRecovery (0.00s)
=== RUN   TestManyTransactions
--- PASS: TestManyTransactions (0.01s)
PASS
ok  	aries	0.535s
```

- `TestCrashRecovery`：验收场景——批量更新中断电，验证崩溃前数据文件不含
  脏页、重启后 Redo 重放已提交数据、Undo 回滚未提交事务、落盘后无残留。
- `TestAbortRollsBack`：显式 Abort 通过 CLR 回滚。
- `TestCleanShutdownReopen`：正常关闭后重开数据完整。
- `TestRepeatedRecovery`：恢复后再次断电，恢复幂等（CLR 不会重复撤销）。
- `TestManyTransactions`：50 个已提交事务 + 1 个失败者交错写 16 页，逐页校验。
