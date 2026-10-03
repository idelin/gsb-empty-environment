# ARIES 崩溃恢复引擎（TypeScript）

数据库经典 ARIES 预写日志（WAL）协议的三阶段崩溃恢复实现：Analysis 重建
TransactionTable / DirtyPageTable，Redo 重复历史（repeat history），Undo 反向扫描
并以 CLR（补偿日志记录）回滚未提交事务。单节点、确定性逻辑时钟（单调 LSN）驱动。

## 运行环境

- Node.js ≥ 22.6（`--experimental-strip-types` 直接运行 TypeScript，零依赖、零构建）
- Node.js ≥ 23.6 可省略该 flag
- 测试使用 Node 内置 `node:test` 运行器

```bash
npm test        # 运行全部测试
npm run demo    # 运行验收场景样例
# 或显式：
node --experimental-strip-types --test test/*.test.ts
node --experimental-strip-types src/demo.ts
```

## 结构

- `src/log-record.ts` — 日志记录类型：UPDATE / COMMIT / ABORT / END / CLR / CHECKPOINT
- `src/wal.ts` — WAL 管理器：确定性 LSN 时钟、flush 持久化、WAL 文件读写与校验
- `src/storage.ts` — 磁盘 + 缓冲池：pageLSN、recLSN(DPT) 跟踪、WAL-before-flush 强制、页闩锁
- `src/locks.ts` — 严格 2PL 键级排他锁（保证 undo 不会覆盖他人已提交写入）
- `src/transaction-manager.ts` — 正常处理：更新/提交/中止（含 CLR）/模糊检查点
- `src/recovery.ts` — ARIES 恢复引擎：IDLE→ANALYSIS→REDO→UNDO→DONE 状态机
- `src/scenario.ts` — 验收场景：5 个事务（2 提交 / 3 未提交）+ 检查点 + 崩溃
- `src/demo.ts` — 可运行样例，打印三阶段恢复全过程

## 核心不变量

- **WAL 协议**：脏页刷盘前，日志必须已持久化到该页 pageLSN（`BufferPool.flushPage` 强制）。
- **Analysis**：从最近 CHECKPOINT 快照出发前向扫描，重建 TT（含状态迁移
  RUNNING→COMMITTED/ABORTED→END 移除）与 DPT（页首次变脏的 recLSN）。
- **Redo**：从 `min(recLSN)` 开始重复历史；跳过不在 DPT 的页、LSN < recLSN 的记录、
  以及 pageLSN ≥ LSN 的页（幂等）；losers 的更新同样重放。
- **Undo**：以全局降序 LSN（最大堆）处理 losers；每条被撤销的 UPDATE 写一条 CLR
  （`undoNextLSN = 被撤销记录的 prevLSN`）；CLR 只重做、永不撤销（沿 undoNextLSN 跳过）；
  事务链耗尽时写 END。
- **无泄漏**：恢复结束后事务表为空、无脏页、无闩锁/锁残留，日志全部持久化。

## 验收场景（src/scenario.ts）

LSN 轨迹：T1、T2 提交；T3、T4、T5 崩溃时仍在运行；LSN 8 处模糊检查点；
P3/P1 被 steal 刷盘（含未提交数据），P2 未刷盘（no-force，丢失已提交数据）。
恢复后终态：`P1={x:100}`、`P2={x:200,y:300}`、`P3={y:400}` —— 已提交数据全部
持久化，未提交修改全部回滚。
