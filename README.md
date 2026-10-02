# ARIES 崩溃恢复引擎 + 确定性事件驱动模拟 (TypeScript)

数据库经典 ARIES 预写日志（WAL）崩溃恢复协议的端到端实现：Analysis / Redo / Undo 三阶段，
配套离散时间事件驱动模拟框架，支持百万步确定性回归测试与故障复现。
纯 TypeScript，零依赖，使用 Node.js 内置测试运行器（Node ≥ 23.6，原生 type-stripping）。

## 运行

```bash
npm test          # 23 个测试（含 1,000,000 步确定性回归）
npm run demo      # 端到端验收场景演示
```

## 结构

- `src/types.ts` — 日志记录类型（UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT）
- `src/log.ts` — LogManager：WAL 追加/刷盘/崩溃丢失，O(1) LSN 索引
- `src/storage.ts` — Disk + BufferPool（pageLSN、recLSN、脏页表）
- `src/txn.ts` — TransactionManager（commit 强制 WAL、abort 走 CLR 链、模糊检查点）+ LatchManager
- `src/recovery.ts` — RecoveryManager：Analysis（重建 DPT/事务表）→ Redo（重复历史）→ Undo（最大 LSN 堆 + CLR 补偿，undoNextLSN 防重复回滚）
- `src/simulation.ts` — 离散时间事件驱动模拟器（种子 PRNG、事件堆、崩溃/恢复事件、提交模型对账、FNV-1a 哈希）
- `src/fault.ts` — 故障复现工具：trace 录制/重放、丢失更新 BUG 确定性复现、临界区故障注入
- `examples/demo.ts` — 验收场景：3 未提交 + 2 已提交事务，Checkpoint 后崩溃，三阶段恢复
- `test/` — 分析/恢复/模拟/故障/并发 5 组共 23 个测试

## 验收场景（examples/demo.ts）

T1 提交 → Checkpoint → T2 提交 → T3/T4/T5 未提交 → 崩溃 → ARIES 恢复：

```
-- Analysis --   checkpoint begin LSN: 5, redo LSN: 1, losers: T3,T4,T5 (RUNNING)
-- Redo --       redone: 6, skipped: 2          (重复历史，含未提交更新)
-- Undo --       CLRs written: 5, txns ended: 5,4,3
最终磁盘: P1=100 P2=50 P3=7 P4=0 P5=0   (已提交持久化，未提交回滚)
```

## 测试覆盖

- Analysis：DPT/事务表精确重建、recLSN 边界、空日志
- Recovery：验收场景、Redo 重复历史、恢复幂等、Undo 中途崩溃再恢复（CLR undoNextLSN）、
  ABORTING 状态断点续回滚、空日志/仅检查点/全提交/崩溃紧随检查点等边界
- Simulation：同种子同哈希、异种子分叉、5 种子崩溃不变量、1,000,000 步黄金哈希回归
- Fault：trace 录制/逐位重放、篡改检测、无锁丢失更新 BUG 复现、临界区故障注入无闩锁泄漏
- Concurrency：闩锁互斥/非属主释放违例、20 种子交错执行可串行化、零泄漏断言
