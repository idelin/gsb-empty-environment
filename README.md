# ARIES 崩溃恢复引擎（Go 标准库实现）

实现数据库经典 **ARIES** 预写日志（WAL）崩溃恢复协议，并在此基础上提供
**轻量级增量快照** 与 **检查点（Checkpoint）归档**，面向海量状态场景：
低停顿（暂停时间与变更集大小成正比，而非库大小）、小开销（只拷贝脏页）。

## 架构

```
aries/
  logrecord.go  日志记录：UPDATE/COMMIT/ABORT/END/CLR/BEGIN_CHKPT/END_CHKPT，二进制编解码
  wal.go        预写日志：追加、刷盘、顺序扫描、归档截断（原子重写）
  page.go       页文件（pageLSN 在页头）+ 缓冲池（脏页表 recLSN、快照变更集）
  txn.go        事务管理器：WAL 规则、提交刷盘、运行时回滚（CLR）、模糊检查点
  recovery.go   恢复引擎：Analysis → Redo → Undo 三阶段
  snapshot.go   增量快照（全量基线 + 变更页增量链）、清单、恢复、检查点归档
cmd/demo/       端到端运行样例（验收场景）
```

### 三阶段恢复

- **Analysis**：从最近一次完整 `END_CHKPT`（崩溃中只写了一半的检查点会被跳过）
  重建事务表与脏页表（DPT），向前扫描至日志末尾，确定 winner/loser 事务。
- **Redo**：历史重复。从 DPT 最小 recLSN 开始重放所有 UPDATE/CLR；
  通过 `DPT 过滤 + recLSN 比较 + 页头 pageLSN 比较` 三重判断保证幂等，
  已在磁盘上的记录被跳过。
- **Undo**：以最大堆按全局 LSN 逆序回滚所有 loser 事务；每个被撤销的
  更新写一条 CLR（携带 `undoNextLSN`），重复恢复时跳过已补偿操作，
  绝不"撤销撤销"。回滚结束为每个 loser 写 END。

### 增量快照与检查点归档

- 首个快照为全量基线；之后每次快照只含**自上次快照以来变更的页**
  （缓冲池在锁内做内存拷贝即释放，磁盘 I/O 不阻塞事务 → 低停顿）。
  零变更快照合法（0 页增量）。
- 快照链由 `manifest.json` 索引；恢复时按代次重放基线 + 增量。
- `ArchiveCheckpoint` 在刷净脏页后，把检查点之前的 WAL 记录移入归档目录
  并原子截断活跃日志（仍活跃事务的记录保留，保证可回滚）；
  `END_CHKPT` 记录本身保留在活跃日志中，分析阶段仍可自举。

## 运行样例

```sh
go run ./cmd/demo
```

输出（节选）：

```
== CRASH: log durable, buffer pool lost ==
== Reboot: ARIES three-phase recovery ==
  analysis: records=9 checkpointLSN=10
  redo:     fromLSN=5 redone=9 skipped=0
  undo:     losers=[3 4 5] CLRs=5
  winners (committed): [2]
--- Recovered page file (t1/t2 committed data kept, t3/t4/t5 rolled back) ---
  page 0: pageLSN=20  data="AAAA0001"
  page 1: pageLSN=25  data="BBBB0001"
  page 2: pageLSN=11  data="CCCC-002"
  page 3: pageLSN=22  data="\x00\x00\x00\x00\x00\x00\x00\x00"
```

## 测试

```sh
go test -race -count=1 -v ./aries/
```

覆盖：

- **验收场景**（`TestCrashRecoveryAcceptance`）：3 个未提交事务（其一崩溃在
  abort 中途、已写 CLR）+ 2 个已提交事务，检查点之后崩溃；三阶段恢复后
  已提交数据持久、未提交修改精确回滚，恢复幂等（二次恢复为零操作）。
- 无检查点恢复、运行时 abort 的 CLR 链。
- 增量快照：全量基线、单页/多页增量、零变更增量、链式恢复、断代续传、
  损坏/缺失/非全量基线等错误边界。
- 检查点归档：截断边界、活跃事务记录保留、空日志、upto=0 拒绝、
  归档后 LSN 单调延续、快照+截断日志的联合恢复。
- 并发安全（`-race` 验证）：多事务并发提交 + 并发快照 + 并发检查点，
  崩溃恢复后全部已提交数据完好；快照页级原子性（无撕裂页）。
- 泄漏断言：完整生命周期前后 goroutine 数一致，`Close` 幂等。
