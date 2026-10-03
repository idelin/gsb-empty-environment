# ARIES 崩溃恢复引擎 + 确定性模拟框架（Go）

纯标准库实现的经典 ARIES（Algorithm for Recovery and Isolation Exploiting
Semantics）预写日志崩溃恢复协议，配套离散时间事件驱动模拟框架，支持
百万步确定性回归测试与故障复现。

## 架构

```
wal/       日志记录类型、零反射二进制编解码、CRC32 校验帧、
           易失/稳定双层 LogStore（内存 + 文件两种实现）
storage/   页式磁盘存储 + 缓冲池（PageLSN / RecLSN / 脏页表）
db/        事务运行时：Begin/Update/Commit/Abort(运行时CLR回滚)/
           Checkpoint/Crash，遵循 WAL 规则（刷页前先刷日志）
recovery/  ARIES 三阶段恢复引擎：
           Analysis 重建 TransactionTable 与 DirtyPageTable（从最近
           Checkpoint 出发）；Redo 历史重复（按 recLSN/PageLSN 精确
           跳过）；Undo 以最大堆按 LSN 逆序回滚 loser 事务并写 CLR
           （CLR 的 UndoNext 保证恢复期再崩溃可续滚，不重复回滚）
sim/       离散时间事件驱动模拟器：事件堆按 (time, seq) 全序、
           单一种子 RNG、页级严格 2PL 锁表工作负载、崩溃/重启事件、
           场景 JSON 序列化与复现（bug reproduction）
cmd/ariesdemo  端到端演示与复现工具
```

关键不变式：ARIES undo 恢复 before-image 的正确性依赖严格 2PL
（未提交数据不会被其他事务覆盖），模拟工作负载内置确定性页级锁表
来保证这一前提。

## 运行

```sh
export GO111MODULE=on        # Go 1.11 需要显式打开 module 模式

# 验收场景演示：2 已提交 + 3 未提交事务、Checkpoint 后崩溃、三阶段恢复
go run ./cmd/ariesdemo

# 确定性模拟（打印场景 JSON，可用于复现）
go run ./cmd/ariesdemo -sim -seed 42 -txns 500 -crash-at 3000

# 故障复现：重放场景文件并校验 steps 与 stateHash 完全一致
go run ./cmd/ariesdemo -reproduce scenario.json

# 测试
go test ./...                # 含百万步回归（慢机器约 1 分钟）
go test -short ./...         # 快速套件
go test -race -short ./...   # 并发安全（无数据竞争）
```

## 演示输出（节选）

```
*** CRASH *** (volatile log tail and buffer pool lost)
=== ARIES recovery: Analysis -> Redo -> Undo ===
analysis: scanned=18 checkpointLSN=12 losers=[3 4 5] winners=[1 2] \
    DPT={p1:recLSN=2 p2:recLSN=3 p3:recLSN=7 p4:recLSN=13 p5:recLSN=18}
redo:     reapplied=4 skipped=4 (history repeated)
undo:     CLRs=5 losers rolled back=[3 4 5]
disk after recovery+flush: {p1=10(lsn=25) p2=20(lsn=22) p3=30(lsn=20) p4=0(lsn=24) p5=0(lsn=19)}
OK: committed data persisted, uncommitted data rolled back
```

## 测试覆盖

- `recovery`：验收场景端到端（精确断言 DPT、losers/winners、CLR 数、
  每页 PageLSN）、Redo 幂等（二次恢复零 CLR）、Undo 中途崩溃注入
  （CLR UndoNext 续滚）、无 Checkpoint、空日志、日志损坏 CRC 检测、
  COMMIT 已持久但 END 丢失的 winner、运行时 Abort 后再崩溃
- `wal`：编解码往返、CRC 帧校验、易失/稳定语义、并发 Append LSN
  唯一性（-race）、文件存储持久化与 Resume
- `db`：运行时 Abort 写 CLR、Commit 强制刷日志、Checkpoint 内容、
  非法事务错误、8 goroutine 并发工作负载 + 崩溃恢复（-race）
- `sim`：同种子双跑步数/事件轨迹/状态哈希完全一致、崩溃恢复正确性
  （对照独立 committed-state 模型与每页 PageLSN）、场景序列化复现与
  篡改检测、**百万步确定性回归**（1,103,487 事件，双跑哈希一致）、
  goroutine 泄漏断言

无泄漏断言：恢复后事务表为空、FlushAll 后脏页表为空、事件队列排空、
goroutine 数不变。

## 测试结果（本机 Go 1.11.4）

```
ok  aries/db        2.499s
ok  aries/recovery  2.187s
ok  aries/sim       29.672s   (含 TestMillionStepRegression: 1,103,487 steps, PASS)
ok  aries/wal       3.216s
# go test -race -short ./... 全部通过，无数据竞争
```

## 故障复现工作流

1. `ariesdemo -sim -seed N` 跑出失败/可疑场景，保存打印的场景 JSON；
2. 任何机器上 `ariesdemo -reproduce scenario.json` 逐步重放同一事件
   序列，校验 steps 与 stateHash，位级一致即复现成功；
3. 测试侧另有故障注入钩子：`recovery.Options.OnCLR` 可在 Undo 中途
   注入崩溃，`MemStore.FlipByte` 可损坏稳定日志验证 CRC 检测。
