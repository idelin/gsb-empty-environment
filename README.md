# ARIES 崩溃恢复引擎(Go)

基于经典 ARIES 协议的预写日志(WAL)与崩溃恢复实现,含三阶段恢复
(Analysis / Redo / Undo + CLR)、模糊检查点与低停顿增量快照归档。
仅使用 Go 标准库。

## 架构

| 文件 | 职责 |
| --- | --- |
| `logrecord.go` | WAL 记录类型(BEGIN/UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT)与二进制编解码 |
| `wal.go` | `LogManager`:追加、按 LSN 精确读、顺序迭代、强制刷盘;LSN 即文件偏移 |
| `disk.go` | `DiskManager`:4KB 页存取,页头 8 字节为 pageLSN |
| `bufferpool.go` | steal/no-force 缓冲池;写盘前遵循 WAL 规则;维护脏页表与变更页集 |
| `db.go` | 事务管理:Begin/Update/Commit/Abort(运行期回滚同样走 CLR) |
| `checkpoint.go` | 模糊检查点:ATT+DPT 写入 CHECKPOINT 记录并更新 master,不刷脏页 |
| `recovery.go` | 三阶段恢复引擎(见下) |
| `snapshot.go` | 增量快照生成、CRC 校验归档、manifest 管理、副本重放 |

## 三阶段恢复

1. **Analysis**:定位最近的 CHECKPOINT 记录,以其携带的 ATT/DPT 为起点
   向前扫描至日志末尾,重建精确的 DirtyPageTable 与 TransactionTable。
2. **Redo(历史重复)**:从 DPT 中最小 recLSN 开始扫描,对 UPDATE/CLR
   记录,若页在 DPT 中且磁盘 pageLSN < 记录 LSN,则重放后像 —
   包括未提交事务的修改,保证幂等。
3. **Undo(反向扫描)**:对败者事务以最大堆按 LSN 全局逆序扫描,
   每条 UPDATE 写一条 CLR(undoNext 指向下一个待撤销记录)并应用前像;
   撤销链走完后写 END。CLR 保证恢复过程本身崩溃后重跑仍正确。

## 增量快照与检查点归档

- 缓冲池维护"自上次快照以来变更的页"集合;快照时仅在短临界区内
  拷贝这些页(通常极少),归档写盘与 manifest 更新均在锁外完成 —— 低停顿。
- 归档格式:`magic | id | baseLSN | upToLSN | count | 页条目... | CRC32`,
  水位严格衔接(`snap[i].BaseLSN == snap[i-1].UpToLSN`),空变更也生成合法空归档。
- 快照后顺带将已归档脏页刷盘,推进磁盘水位,使恢复工作量有界。
- `RestoreSnapshots` 按序把归档重放到全新磁盘(副本/PITR),CRC 校验防篡改。

## 运行

```sh
go test -race ./...   # 全部测试(含竞态检测)
go run ./cmd/demo     # 端到端演示:5 事务检查点后崩溃 -> 三阶段恢复
```

## 测试覆盖

- `TestCrashRecoveryThreePhases`:验收场景 —— 3 未提交 + 2 已提交事务,
  检查点后崩溃;断言 DPT/ATT 重建、Redo 起点早于检查点、5 条 CLR、
  已提交数据持久、未提交数据逐字节回滚、磁盘 pageLSN 与日志严格一致。
- `TestRecoveryIdempotent` / `TestRecoveryWithoutCheckpoint` / `TestCLRUndoChain` / `TestAbortRollback`。
- 快照边界:增量只含变更页、空快照、水位衔接、manifest 断点续接、
  CRC 篡改检测、空目录恢复、快照使恢复工作量有界。
- `TestConcurrentWorkloadWithSnapshots`(-race):8 并发事务 + 后台快照/检查点,
  崩溃恢复后磁盘与日志一致。
- `TestNoGoroutineLeak`:多轮完整生命周期后 goroutine 数严格不增。
