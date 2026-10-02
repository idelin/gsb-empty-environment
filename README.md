# ARIES 崩溃恢复引擎 + 多租户配额公平调度器 (TypeScript)

纯 TypeScript 实现，零依赖，直接使用 Node.js (>=23.6) 原生类型擦除运行，
测试基于 Node 内置 `node:test` 运行器。

## 模块

| 文件 | 说明 |
| --- | --- |
| `src/wal.ts` | 预写日志：UPDATE / CLR / COMMIT / ABORT / END / CHECKPOINT 记录，JSONL 持久化，单调递增 LSN |
| `src/storage.ts` | 页式存储，每页携带 `pageLSN`（redo 幂等的关键） |
| `src/recovery.ts` | ARIES 三阶段恢复：Analysis 重建 TT/DPT 并识别 loser 事务；Redo 从最小 recLSN 重放历史（`pageLSN >= lsn` 跳过）；Undo 按 LSN 逆序回滚 loser，写 CLR（含 `undoNextLSN` 链）与 END |
| `src/scheduler.ts` | 多租户调度器：每租户独立 FIFO 队列 + 并发上限 + 排队配额（隔离），Deficit Round Robin 按 `weight * quantum` 记账派发（加权公平），任务失败/完成均释放许可（无泄漏） |

## 运行

```bash
npm test        # node --test（18 个断言严格的测试）
npm run example # 端到端演示：构造 WAL -> 崩溃 -> 三阶段恢复 -> DRR 调度
```

## 验收场景

测试构造包含 **3 个未提交事务（T3/T4/T5）与 2 个已提交事务（T1/T2）**、
在 CHECKPOINT 之后崩溃的 WAL（LSN 1–12），恢复引擎执行三阶段扫描：

- **Analysis**：从 checkpoint（LSN 8）起重建事务表与脏页表，识别 loser `[3,4,5]`；
- **Redo**：从最小 recLSN=1 重放历史，跳过已持久化的页（`pageLSN` 判断）；
- **Undo**：逆序 `[12,11,10,9,5]` 回滚全部 loser 更新，写 5 条 CLR + 3 条 END。

最终已提交数据（`T1-data`/`T2-data`）持久化，未提交修改全部回滚；
恢复幂等（二次恢复无副作用），且恢复中途再次崩溃可由 CLR 重做完成。

调度器测试覆盖：权重公平（2:1 → AAB 模式、3:1 吞吐比例）、并发上限隔离、
慢租户不阻塞快租户、排队配额拒绝（`QuotaExceededError`）、失败任务许可不泄漏、
deficit 空闲清零、8 租户 × 1600 任务并发压测零泄漏。
