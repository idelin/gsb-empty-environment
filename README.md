# ARIES 崩溃恢复引擎 + 多租户 DRR 公平调度器 (TypeScript)

两个组件，纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试运行器
(`node:test`) 验证。源码仅使用可擦除类型语法（erasable syntax），
通过 Node >= 22.6 的 `--experimental-strip-types` 直接运行，无需编译步骤。

## 组件一：ARIES 预写日志分析与崩溃恢复

经典 ARIES 协议（Mohan et al.）三阶段重启恢复：

- **Analysis**（`src/recovery.ts` `analysis()`）：从最近的 CHECKPOINT
  （含 ATT/DPT 快照）出发向前扫描，重建事务表（TransactionTable）与
  脏页表（DirtyPageTable），确定 `redoStartLSN = min(recLSN)`。
- **Redo**（`redo()`）：从 `redoStartLSN` 重复历史，对 UPDATE 与 CLR
  均重放 after-image；通过 `pageLSN >= lsn` 检查保证幂等。
- **Undo**（`undo()`）：对所有 loser 事务按 LSN 逆序回滚，每个被撤销的
  UPDATE 写入一条 CLR（补偿日志），CLR 携带 `undoNextLSN` 指向被撤销记录
  的 `prevLSN`，使恢复过程中再次崩溃也不会重复回滚；事务链走到 BEGIN
  时写入 END 记录。

支撑模块：

- `src/wal.ts` — 预写日志管理器（JSON-lines 文件，同步强制落盘）。
- `src/storage.ts` — 模拟磁盘，页携带 `pageLSN`（steal / no-force 模型）。
- `src/scenario.ts` — 验收场景构造器：3 个未提交事务（T3/T4/T5）+
  2 个已提交事务（T1/T2），中间一次 CHECKPOINT，崩溃前仅部分脏页落盘
  （含被 steal 的未提交数据与已提交但未 flush 的数据）。

## 组件二：多租户隔离与配额公平调度器

`src/scheduler.ts`：

- **隔离**：每租户独立 FIFO 队列、独立配额（窗口内最大任务数）、
  独立并发上限、独立队列上限（溢出在提交侧同步拒绝，故障不扩散）。
- **公平**：Deficit Round Robin —— 每轮为每个有积压的租户记入
  `quantum × weight` 赤字，任务仅在赤字覆盖其 cost 时派发；
  未用完的赤字可结转，队列排空时清零；被自身配额/并发上限阻塞的
  租户不累积赤字。
- **并发安全**：单线程事件循环下的原子检查-派发；`drain()` 保证所有
  队列清空、无任务泄漏（submitted == completed + failed + rejected）。

## 运行

需要 Node.js >= 22.6（`--experimental-strip-types`）。

```bash
# 运行测试（node:test 内置运行器）
npm test
# 或显式：
node --experimental-strip-types --test 'test/*.test.ts'

# 运行演示样例（崩溃场景 + 三阶段恢复 + DRR 调度）
npm run demo
```

## 演示输出（节选）

```
--- Phase 1: Analysis ---
  T1: COMMITTED lastLSN=9   T2: COMMITTED lastLSN=13
  T3/T4/T5: RUNNING (losers)
  redoStartLSN=2
--- Phase 2: Redo (repeat history) ---
  redone LSNs: [8, 15]          # 已提交未落盘的 P3、loser 的 P5 均被重放
--- Phase 3: Undo (losers, with CLRs) ---
  undone update LSNs: [15, 11, 6]
  CLR LSNs written:   [16, 18, 20]
--- Disk after recovery ---
  P1: 'A1' OK  P2: 'B1' OK  P3: 'C1' OK  P4: 'D1' OK  P5: 'E' OK
```

## 测试结果

17 个测试全部通过：

```
# tests 17
# pass 17
# fail 0
```

恢复引擎覆盖：Analysis 表重建（含/无 checkpoint）、Redo 幂等性、
Undo 逆序 CLR 链、undo 中途崩溃后 CLR 跳过不重复回滚、端到端
5 事务验收场景、无泄漏断言（每个 loser 恰一条 END、每个 loser
UPDATE 恰一条 CLR）。

调度器覆盖：权重比例公平、赤字结转与清零、配额窗口上限与滚动重置、
配额阻塞不累积赤字、洪泛租户隔离（队列上限拒绝）、并发上限、
非法配置/未知租户/非法 cost 边界、失败任务计数不泄漏、
1500 任务并发提交严格无泄漏断言（每任务恰好执行一次、
inFlight 归零、配额不超限）。
