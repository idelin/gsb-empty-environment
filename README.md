# ARIES 崩溃恢复引擎(Go 标准库实现)

数据库经典 **ARIES**(Algorithm for Recovery and Isolation Exploiting Semantics)
预写日志崩溃恢复协议实现,包含三阶段恢复引擎与异常注入容错验证。

## 架构

| 文件 | 职责 |
|---|---|
| `record.go` | WAL 记录类型(BEGIN/UPDATE/COMMIT/ABORT/CLR/END/CHECKPOINT)与二进制编解码,CRC32 校验 |
| `wal.go` | 预写日志:追加(互斥锁保证 LSN 分配无竞争)、扫描、单点读取;打开时截断撕裂尾部 |
| `pagestore.go` | 页式存储:4KB 页(8B pageLSN + 数据),页级原子写 |
| `recovery.go` | **ARIES 三阶段恢复引擎**:Analysis / Redo / Undo |
| `txn.go` | 事务管理器(崩溃前正常运行路径):缓冲池、steal 刷盘、模糊检查点 |
| `fault.go` | 故障注入器:突发断电(撕裂写)、磁盘满(ENOSPC)、可重启 |
| `replicate.go` | 日志复制:网络丢包下基于 LSN 间断检测 + 重传的自愈 |
| `cmd/demo/` | 运行样例 |

## 三阶段恢复

1. **Analysis** — 从最后一个 Checkpoint 快照(而非合并旧表)重建
   TransactionTable 与 DirtyPageTable,扫描至日志末尾,识别崩溃时的
   loser 事务(RUNNING / ABORTING)。
2. **Redo** — 历史重复:从 DPT 最小 recLSN 起正向扫描,对所有
   UPDATE/CLR 记录,若页在 DPT 中且 `pageLSN < lsn` 则重放 after-image,
   精确恢复崩溃瞬间的物理状态(包括未提交数据与已写的 CLR)。
3. **Undo** — 按 LSN 降序反向扫描 loser 事务链,每个被撤销的 UPDATE
   写入一条 CLR(`undoNext` 指向下一待撤销记录),事务链走到 BEGIN
   时写 END。CLR 保证崩溃重启后恢复**幂等**,不会重复回滚。

## 异常注入与容错

- **突发断电**:日志写倒计时归零后注入撕裂写(半条记录落盘)并置机器
  为死亡状态;重启时 WAL 打开逻辑通过 CRC 校验定位崩溃点并截断。
  恢复过程中(Undo 写 CLR 时)再次断电,重启后依靠 CLR 的 `undoNext`
  精确续跑,每个 loser 恰好一条 END。
- **磁盘满**:所有写返回 ENOSPC,恢复引擎干净地报错退出,不破坏任何
  状态;故障解除后重跑恢复即可收敛。
- **网络丢包**:日志复制按 40% 概率丢包,接收方通过 LSN 不连续检测
  空洞,发送方重传直至副本与主库逐字节一致。

## 运行样例

```console
$ go run ./cmd/demo
=== Phase 0: normal operation ===
T1 committed+END, T2 committed(no END), T3 running, T4 aborting, T5 running
checkpoint taken; pages 1,2 flushed; *** POWER FAILURE ***

=== Phase 1: Analysis ===
Transaction Table:
  txn=2   status=COMMITTED  lastLSN=24907
  txn=3   status=RUNNING    lastLSN=25099
  txn=4   status=ABORTING   lastLSN=41663
  txn=5   status=RUNNING    lastLSN=54086
Dirty Page Table:
  page=1   recLSN=25099
  page=2   recLSN=45857
  page=3   recLSN=16678
  page=4   recLSN=33381
  page=5   recLSN=54086
=== Phase 2: Redo (repeat history) ===
re-applied 6 log records

=== Phase 3: Undo (loser rollback with CLRs) ===
undone updates=3  CLRs written=3  ENDs written=4

=== Final durable state ===
page 1 = "A1"     # T1 已提交保留,T3 未提交的 "A3" 已回滚
page 2 = "B1"     # T1 已提交保留,T5 未提交的 "B5" 已回滚
page 3 = "C2"     # T2 已提交,Redo 恢复
page 4 = ""       # T4 中止
page 5 = ""       # T5 未提交
```

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestConcurrentWorkload          # 8 goroutine 并发写 WAL + 并发 checkpoint,-race 无数据竞争,无 goroutine 泄漏
--- PASS: TestConcurrentWorkload (4.27s)
=== RUN   TestPacketLoss_LogShipping      # 40% 丢包下日志复制收敛,副本逐字节一致
--- PASS: TestPacketLoss_LogShipping (0.59s)
=== RUN   TestRecover_AcceptanceScenario  # 验收边界:3 未提交 + 2 已提交,checkpoint 后崩溃
--- PASS: TestRecover_AcceptanceScenario (0.17s)
=== RUN   TestRecover_Idempotent          # 二次恢复为零操作(pageLSN/CLR 幂等)
--- PASS: TestRecover_Idempotent (0.15s)
=== RUN   TestRecover_EmptyAndCheckpointOnly  # 空 WAL / 仅 checkpoint / checkpoint 后立即崩溃
--- PASS: TestRecover_EmptyAndCheckpointOnly (0.04s)
=== RUN   TestPowerLoss_TornTail          # 运行中断电:撕裂尾部被截断,loser 回滚
--- PASS: TestPowerLoss_TornTail (0.03s)
=== RUN   TestDiskFull_DuringRecovery     # 恢复中磁盘满:干净失败,可重试收敛
--- PASS: TestDiskFull_DuringRecovery (0.17s)
=== RUN   TestPowerLoss_DuringUndo        # Undo 写 CLR 时断电:重启后续跑,每事务恰好一条 END
--- PASS: TestPowerLoss_DuringUndo (0.16s)
PASS
ok  	aries	7.044s
```

无泄漏断言:并发测试前后 `runtime.NumGoroutine()` 一致;所有文件句柄
随测试关闭;恢复引擎不驻留后台 goroutine。
