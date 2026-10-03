# aries — ARIES 崩溃恢复引擎与形式化约束巡检仪（Go）

纯标准库实现的经典 ARIES（Algorithm for Recovery and Isolation Exploiting
Semantics）崩溃恢复协议，外加一个离线完整性巡检仪，用于主动发现悬挂引用、
孤立环路与缓冲/磁盘数据倾斜。

## 结构

```
wal/        预写日志：记录类型、CRC 帧编解码、并发安全的 LogManager、checkpoint master
storage/    页式磁盘存储（pageLSN 头）与带脏页跟踪的缓冲池
recovery/   ARIES 三阶段重启恢复：Analysis（重建 ATT/DPT）、Redo（重复历史）、
            Undo（反向扫描、CLR 补偿、undoNext 幂等续滚）；System 事务处理门面
inspect/    形式化约束巡检与不变式断言仪：独立于恢复引擎重新推导全部不变式
cmd/ariesdemo/  端到端演示：构造崩溃场景 → 三阶段恢复 → 巡检 → 校验最终状态
```

## 验收场景

`recovery.BuildCrashScenario` 构造规范要求的崩溃现场：5 个事务，checkpoint
之后 t1、t2 提交，t3/t4/t5 未提交即崩溃（t3 还覆盖了已提交 t1 的页面）。
恢复后必须满足：已提交修改持久化、未提交修改全部回滚（含对被覆盖提交页的
精确还原）。

## 巡检不变式

| 检查 | 发现的问题 |
|---|---|
| `CheckChains` | PrevLSN 悬挂引用、跨事务链接、checkpoint ATT/DPT 悬空表项 |
| `CheckCLRLinks` | CLR undoNext 指向缺失/他事务/非单调记录 |
| `CheckAcyclic` | PrevLSN/undoNext 链上的孤立环路（会使 Undo 死循环） |
| `CheckDPT` | 独立重算 DPT 并与分析结果比对，校验 recLSN 指向真实 UPDATE/CLR |
| `CheckPersistence` | 已提交数据未落盘、已结束败者事务存在未补偿更新 |
| `CheckSkew` | 缓冲页落后于磁盘、脏标志无未决变更、干净帧与磁盘偏离 |

`Inspector.AssertClean` 是不变式断言仪：任何违例直接 panic，用于测试与演示。

## 运行

```sh
go run ./cmd/ariesdemo   # 端到端演示
go test -race ./...      # 全部测试（含竞态检测）
```

## 运行样例（`go run ./cmd/ariesdemo`，节选）

```
=== Phase 1: normal processing (5 txns, checkpoint, then crash) ===
   LSN=8   CHECKPOINT att=map[1:6 2:7 3:3 4:4 5:5] dpt=map[1:6 2:7]
   LSN=11  COMMIT txn=1 prev=10
   LSN=14  COMMIT txn=2 prev=13
   LSN=18  UPDATE txn=3 prev=9 page=3 ...        <- t3/t4/t5 未提交即崩溃
=== Phase 3: ARIES restart recovery ===
-- analysis: transaction table
  txn 3 status=RUNNING lastLSN=18
  txn 4 status=RUNNING lastLSN=16
  txn 5 status=RUNNING lastLSN=17
-- redo: repeated 6 log record(s) (history repeated)
-- undo: losers=[3 4 5] winners=[1 2], wrote 4 CLR(s) and 3 END record(s)
=== Phase 4: invariant inspection ===
  all invariants hold: no dangling references, no orphan loops, no skew
=== Phase 5: final durable state vs expectation ===
  P1 = "p1-t1" (expected "p1-t1")   <- t3 覆盖已回滚
  P2 = "p2-t2b" (expected "p2-t2b") <- t2 提交后更新已重做
recovery verified: committed txns [1 2] persisted; losers [3 4 5] rolled back
```

## 测试结果（`go test -race ./...`，Go 1.24，darwin/arm64）

```
ok  aries/inspect   15 tests — 巡检仪全部边界条件 + 并发检查
ok  aries/recovery  10 tests — 验收场景、幂等重启、崩溃中途中止续滚、
                              无 checkpoint、空日志、并发事务、goroutine 无泄漏
ok  aries/storage    6 tests — 页读写、缓冲池生命周期、并发访问
ok  aries/wal        9 tests — 编解码往返、CRC 损坏检测、并发追加、
                              flush 单调性、master 记录
```

共 40 个测试，全部通过，`-race` 无数据竞争，`TestNoGoroutineLeak` 断言严格无泄漏。
