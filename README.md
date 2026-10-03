# ARIES 崩溃恢复引擎（TypeScript）

数据库经典 **ARIES** 预写日志（WAL）分析与崩溃恢复 Redo/Undo 引擎，含异常注入与崩溃恢复容错性验证。纯 TypeScript 实现，零运行时依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 环境要求

- **Node.js >= 22.6**（利用原生 type-stripping 直接运行 `.ts`；推荐 23.x，见 `.nvmrc`）
- TypeScript 编译器（仅 `npm run typecheck` 需要）

## 快速开始

```bash
nvm use            # 或自行切换到 Node >= 22.6
npm run demo       # 运行样例：构建 WAL -> 断电 -> 三阶段恢复
npm test           # 运行全部 34 个内置测试
npm run typecheck  # tsc --noEmit 严格类型检查
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/types.ts` | LSN、UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT 日志记录、事务表、脏页表 |
| `src/disk.ts` | 模拟持久存储；ENOSPC 故障注入；校验和验证与撕裂写截断（自愈） |
| `src/logManager.ts` | WAL 管理：LSN 分配、日志缓冲、force-at-commit、pageLSN 前刷日志 |
| `src/bufferPool.ts` | STEAL/NO-FORCE 缓冲池，跟踪 pageLSN 与 recLSN |
| `src/lockManager.ts` | 严格 2PL 排他页锁 |
| `src/recovery.ts` | **ARIES 三阶段**：Analysis（重建 TT/DPT）→ Redo（历史重复）→ Undo（CLR 补偿反向扫描） |
| `src/system.ts` | 事务系统：begin/update/commit/abort、模糊检查点、crash/recover |
| `src/fault.ts` | 异常注入：突发断电、磁盘满、指定 LSN 崩溃（可注入恢复过程内部） |
| `src/replication.ts` | 丢包日志传输通道 + 副本端 LSN 缺口截断自愈 |
| `src/scheduler.ts` | 确定性协作调度器：轮转交错、死锁检测（wait-for 环）与最年轻事务牺牲 |

## 验收场景（`examples/demo.ts` 与 `tests/acceptance.test.ts`）

构造包含 **2 个已提交事务（T1、T2）与 3 个未提交事务（T3、T4、T5）** 的 WAL，在 Checkpoint 之后崩溃：

```
[LSN  1] UPDATE  T1 P1 0->100      [LSN  8] UPDATE  T3 P1 100->111
[LSN  2] UPDATE  T2 P3 0->300      [LSN  9] BEGIN_CHECKPOINT
[LSN  3] UPDATE  T1 P2 0->200      [LSN 10] END_CHECKPOINT
[LSN  4] COMMIT  T1                [LSN 11] UPDATE  T4 P2 200->222
[LSN  5] END     T1                [LSN 12] UPDATE  T5 P4 0->444
[LSN  6] COMMIT  T2                [LSN 13] UPDATE  T5 P5 0->555
[LSN  7] END     T2
```

崩溃前将含未提交数据（P1=111）的脏页 STEAL 到磁盘。恢复引擎执行三阶段扫描：

- **Analysis**：从 LSN 10 的 END_CHECKPOINT 快照出发，重建 TT=`{T3,T4,T5}`、DPT=`{P1:1,P2:3,P3:2,P4:12,P5:13}`，redoLSN=1；
- **Redo**：历史重复，重放 5 条 UPDATE（含未提交事务的修改），跳过已在磁盘上的 2 条；
- **Undo**：按 LSN 逆序回滚 3 个未提交事务，写入 4 条 CLR（undoNextLSN 链保证崩溃重入不重复回滚），并补齐 END。

最终磁盘状态：`P1=100, P2=200, P3=300`（已提交持久化），`P4=P5=0`（未提交回滚）。

## 测试覆盖（34 个用例，全部通过）

- `tests/analysis.test.ts` — 空日志、全日志扫描、检查点快照合并、COMMITTING/ABORTING 状态、END 移除
- `tests/redo.test.ts` — 历史重复（含 CLR）、pageLSN 跳过、最小 recLSN 起点、幂等性
- `tests/undo.test.ts` — CLR 链完整性、多 loser 全局逆序、winner 补 END、undoNextLSN 防重复回滚
- `tests/acceptance.test.ts` — 验收边界场景、恢复幂等（二次崩溃零新增 CLR）、恢复后系统可用
- `tests/faultInjection.test.ts` — 断电（提交前/提交后/撕裂写/undo 中途）、磁盘满（提交重试、WAL 协议、恢复期 ENOSPC 自愈）、网络丢包（副本缺口截断 + 前缀一致恢复）
- `tests/concurrency.test.ts` — 交错递增无丢失更新、死锁环检测与牺牲、独立页并发、显式中止隔离、崩溃后精确恢复已提交集合、锁下读改写原子性

每个恢复用例均断言**严格无泄漏**：事务表为空、无残留锁、无 pin 页、日志缓冲全部落盘、每个事务都有 END 记录、日志 LSN 连续且校验和完整。

## 运行样例输出

```
=== ARIES crash recovery demo ===
--- Phase 1+2+3: restart recovery (Analysis -> Redo -> Undo) ---
Analysis: checkpoint at LSN 10, redoLSN=1
Analysis: txnTable = { T3:RUNNING@8, T4:RUNNING@11, T5:RUNNING@13 }
Analysis: dirtyPageTable = { P1:recLSN=1, P2:recLSN=3, P3:recLSN=2, P4:recLSN=12, P5:recLSN=13 }
Redo: applied=5 skipped=2 (history repeated)
Undo: losers=[T3,T4,T5] winners=[] CLRs written=4
--- final page values on disk ---
  P1 = 100   P2 = 200   P3 = 300   P4 = 0   P5 = 0
RESULT: committed data persisted, uncommitted data rolled back. OK
Leak check: txnTable empty=true, pinned=0, locks=0, bufferedLog=0
```

## 测试结果

```
ℹ tests 34
ℹ pass 34
ℹ fail 0
```
