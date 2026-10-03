# ARIES 崩溃恢复引擎 + 确定性故障复现模拟器 (Go)

纯标准库实现的经典 ARIES 数据库崩溃恢复协议，以及一个离散时间、
事件驱动的确定性模拟框架，用于百万步回归测试与 BUG 复现。

## 架构

```
wal/        预写日志：记录类型(UPDATE/COMMIT/ABORT/CLR/END/CHECKPOINT)、
            确定性二进制编码、追加式日志文件、撕裂写(torn tail)容错
storage/    页式稳定存储模型（pageLSN 版本化页镜像）
db/         数据库门面：steal/no-force 缓冲池、WAL 优先、模糊检查点、
            运行时 Abort(写 CLR)、Flush、Crash(丢失全部易失状态)
recovery/   ARIES 三阶段恢复引擎
sim/        离散时间事件驱动模拟器 + 确定性工作负载生成器
cmd/ariesdemo  端到端验收场景演示
```

## ARIES 三阶段

1. **Analysis** — 从最后一个 Checkpoint 出发正向扫描，重建崩溃瞬间的
   DirtyPageTable（页 → recLSN）与 TransactionTable（事务 → lastLSN/状态），
   识别 loser（未提交）事务。
2. **Redo** — 从 DPT 中最老的 recLSN 开始"重复历史"：对每条
   UPDATE/CLR，仅当页在 DPT 中、`DPT recLSN ≤ LSN` 且
   `磁盘 pageLSN < LSN` 时重放 after-image（幂等）。
3. **Undo** — 以最大堆按 LSN 逆序回滚所有 loser：每条 UPDATE 写一条
   CLR（补偿日志，含 undoNextLSN 指向链中前驱），应用 before-image；
   遇到 CLR 跳到 undoNext（绝不撤销 CLR，崩溃中恢复可安全重入）；
   链走到头写 END。

## 确定性模拟框架

- 单种子 RNG + 按 `(time, seq)` 排序的事件堆 → 同种子逐比特复现。
- `sim.RunWorkload` 生成事务负载（提交/中止/刷页/检查点/定时崩溃），
  事务页空间互不相交（等价于严格 2PL 的并发保证），并维护
  "已提交模型"作为恢复正确性的 ground truth。
- 百万步回归：`TestMillionStepDeterminism` 两次运行 1,000,000 事件，
  断言状态完全一致。
- 故障复现：`TestFaultReproduction` 同种子两次"崩溃+恢复"结果逐比特
  相同，且 25 个不同种子全部通过恢复正确性校验。

## 运行

```bash
go test ./...          # 全部测试
go test ./... -race    # 并发安全（数据竞争检测）
go run ./cmd/ariesdemo # 端到端演示：5 事务 + checkpoint + 崩溃 + 恢复
```

## 验收场景（cmd/ariesdemo 输出摘要）

- 事务 1/2/3 未提交，事务 4/5 已提交；Checkpoint 后事务 3 继续写入；
  脏页 10/12 被 steal 到磁盘（含未提交数据），已提交页 13 故意不刷盘。
- 崩溃后恢复：Analysis 定位 checkpoint(LSN 10)、loser={1,2,3}；
  Redo 重放 4 条记录；Undo 写 5 条 CLR 回滚 5 处未提交修改。
- 最终磁盘：页 12=txn4、页 13=txn5（已提交持久化），
  页 10/11=零镜像（未提交已回滚）。

## 测试覆盖

| 测试 | 覆盖点 |
|---|---|
| `recovery.TestEndToEndCrashRecovery` | 验收边界：3 未提交 + 2 已提交 + checkpoint 后崩溃；恢复幂等性 |
| `recovery.TestEmptyWAL` / `TestNoCheckpoint` | 空日志、无检查点边界 |
| `recovery.TestAllCommittedNoUndo` | 无 loser 时 Undo 完全跳过 |
| `recovery.TestCrashDuringUndo` | 恢复过程中再次崩溃：CLR 不被重复撤销，从 undoNext 续跑 |
| `recovery.TestRuntimeAbortThenCrash` | 运行时 Abort 留下的 CLR 链正确处理 |
| `recovery.TestRedoRespectsPageLSN` | Redo 不覆盖更新的页镜像 |
| `recovery.TestConcurrentTransactions` | 多 goroutine 并发事务 + 崩溃恢复一致性 |
| `recovery.TestNoGoroutineLeak` / `sim.TestSimNoGoroutineLeak` | 严格无 goroutine 泄漏 |
| `wal.*` | 编解码往返、检查点编码确定性、重开续写、撕裂写容错、并发追加 LSN 唯一 |
| `sim.TestMillionStepDeterminism` | 百万步确定性回归 |
| `sim.TestFaultReproduction` | 种子化故障复现 + 25 种子恢复正确性 |

测试结果（Go 1.24，darwin/arm64）：

```
ok  aries/recovery   (含 -race)
ok  aries/sim        (含 -race)
ok  aries/wal        (含 -race)
```
