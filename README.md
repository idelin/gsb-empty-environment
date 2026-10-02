# aries — ARIES 崩溃恢复引擎 + 多节点冲突仲裁器（Go，仅标准库）

实现数据库经典 ARIES 崩溃恢复协议的三阶段重启恢复，以及一个支撑多执行体
并发竞争的、无死锁的冲突仲裁器（wound-wait 锁管理器）。

## 结构

| 文件 | 内容 |
|---|---|
| `wal.go` | 预写日志：CRC 帧格式、7 种记录类型（BEGIN/UPDATE/COMMIT/ABORT/CLR/END/CHECKPOINT）、定位追加写、撕裂尾部容忍扫描 |
| `page.go` | 定长数据页（含 pageLSN）与模拟持久化磁盘 |
| `db.go` | 事务管理器：steal/no-force 缓冲池、WAL 先行、commit 强制刷日志、检查点（刷脏页 + 记录 DPT/TT 快照） |
| `recovery.go` | 三阶段恢复引擎：Analysis 重建脏页表/事务表，Redo 从最小 recLSN 重复历史，Undo 用最大堆按 LSN 逆序回滚 loser 并写 CLR（undoNext 链，崩溃中恢复安全） |
| `arbiter.go` | 冲突仲裁器：单互斥锁保证线性一致授权序；wound-wait（老者伤害幼者，幼者只等老者）从等待图结构上杜绝死锁；FIFO 公平队列、S 锁共授、S→X 升级、无后台 goroutine |

## 运行样例

```sh
go run ./cmd/ariesdemo
```

演示构造 2 个已提交（T1、T2）+ 3 个未提交（T3、T4、T5）事务，在
Checkpoint 之后崩溃，随后执行三阶段恢复，最后压测仲裁器。关键输出：

```
disk at crash: P1=111 P2=200 P3=0 (P1 holds uncommitted 111!)
analysis: checkpoint=true DPT=map[2:550 3:778] committed=[1 3] losers=[2 4 5]
redo:     re-applied 4 records (LSN [550 778 900 969]), skipped 0 already-durable
undo:     rolled back LSN [969 900 778 351] via 4 CLRs
disk after recovery: P1=100 P2=300 P3=0   # 与期望完全一致
nodes=8 committed=3733 ... balance sum=16000 (invariant 16000) leaked entries=0 txns=0
```

## 测试

```sh
go test -race -count=1 -v ./...
```

17 个测试全部通过（`-race` 下）：

- 恢复：`TestRecoverCrashScenario`（验收场景：3 未提交 + 2 已提交，checkpoint
  后崩溃，断言 DPT/loser 集合、redo 4 条、undo 4 条 CLR、最终页内容）、
  `TestRecoveryIdempotent`（恢复后再崩溃，二次恢复为零操作）、
  `TestCLRChainSkipsUndoneWork`（CLR 的 undoNext 链完整性）、
  `TestWALCorruptTail`（撕裂尾部）、`TestWALRoundTrip`（全记录类型编解码）。
- 仲裁器边界：S 兼容、X 互斥、FIFO 公平、S 共授、老者伤害幼者、幼者等待
  老者、等待者被伤害时唤醒、重入、升级冲突、Abort 唤醒。
- 并发安全：`TestConcurrentArbiter`（8 节点 × 200 事务转账守恒不变量 +
  30s 死锁看门狗）、`TestNoGoroutineLeak`（goroutine 数回归基线、锁表
  状态归零，严格无泄漏）。

最近一次运行结果：

```
--- PASS: TestConcurrentArbiter (committed=1470 retried(wounded)=6)
--- PASS: TestRecoverCrashScenario
...（共 17 项）
PASS
ok  	aries	2.710s
```
