# ARIES 崩溃恢复 + 分层分片局部性感知路由（TypeScript）

零依赖 TypeScript 实现，包含两个组件：

1. **ARIES 预写日志（WAL）与崩溃恢复引擎** — 经典三阶段协议：Analysis 重建
   TransactionTable / DirtyPageTable，Redo 重复历史（含 CLR），Undo 反向扫描并以
   CLR（补偿日志记录）回滚所有未提交事务。
2. **分层分片与局部性感知路由引擎** — 虚拟分片（rendezvous/HRW 哈希）、
   region → zone → node 分层拓扑、副本层级感知放置、局部性感知路由与批量归组，
   最小化跨节点 / 跨可用区通信。

## 运行要求

- Node.js ≥ 23.6（原生 type-stripping，直接运行 `.ts`），或 Node ≥ 22.6 使用
  `npm run test:node22`（`--experimental-strip-types`）。
- 无任何第三方依赖；测试使用 Node 内置 `node:test`。

```bash
npm test        # node --test "test/*.test.ts"
npm run demo    # node examples/demo.ts
```

## 目录结构

```
src/
  aries/
    types.ts      日志记录类型：BEGIN/UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT
    wal.ts        WalWriter（LSN 分配、JSONL 落盘、fsync）与 readWal
    storage.ts    页 / 缓冲页 / 磁盘页表
    engine.ts     Database：事务执行、steal/no-force 缓冲、checkpoint、crash()
    recovery.ts   RecoveryManager：Analysis → Redo → Undo（最大堆反向扫描）
  sharding/
    hash.ts       FNV-1a 32 位哈希
    topology.ts   分层拓扑与代价模型（zone=1, region=5, cross-region=20）
    ring.ts       ShardRing：虚拟分片 + HRW 分配 + 层级感知副本放置
    router.ts     LocalityAwareRouter：路由、批量归组、故障转移、异步互斥、drain
test/             node:test 测试（19 个用例）
examples/demo.ts  端到端运行样例
```

## ARIES 实现要点

- **Analysis**：从最近的 CHECKPOINT 出发扫描至日志尾，重建事务表
  （RUNNING/COMMITTED/ABORTING，END 移除）与脏页表（pageId → recLSN）。
- **Redo**：从 `min(recLSN)` 开始重复历史；仅当记录在 DPT 中、
  `lsn >= recLSN` 且 `page.pageLSN < lsn` 时重放 after-image —— 幂等。
- **Undo**：对 Analysis 末仍未结束的事务（loser），用最大堆按 LSN 从大到小
  反向扫描；对 UPDATE 写 CLR（`undoNextLSN = prevLSN`）并恢复 before-image；
  遇到 CLR 跳转到 `undoNextLSN`，避免重复回滚；链到 BEGIN 时写 END。
  结束后刷全部脏页并 fsync WAL。
- 正常运行期 `abort()` 与恢复期 Undo 共用同一套 CLR 语义；
  `commit` 只刷日志不刷页（no-force），`flushPage` 可随时偷页（steal）。

## 分片与路由实现要点

- **虚拟分片**：`shardOf(key) = FNV1a(key) mod shardCount`，分片数远大于节点数，
  再均衡时只移动必须移动的分片（HRW 天然最小迁移）。
- **层级感知副本放置**：每个分片按 HRW 排名选副本，优先跨 region、再跨 zone，
  不足时降级填充。
- **局部性感知路由**：在存活副本中选网络代价最小者（同 zone < 同 region < 跨
  region），并列时按节点 id 确定性裁决；`routeBatch` 将按键路由归组为每节点
  一次 RPC，大幅降低跨节点通信。
- **并发安全**：拓扑变更经异步互斥串行化并单调递增路由版本；路由读取基于
  不可变快照；`withRouted`/`drain` 跟踪在途操作，保证无泄漏（in-flight、
  互斥、等待队列全部归零）。

## 验收场景（test/aries.test.ts）

构造 WAL：T1、T2 已提交，T3、T4、T5 未提交，LSN 5 处 checkpoint，崩溃前
将未提交的 `A=999` 偷页落盘（steal）。恢复后断言：

- Analysis：事务表 `{T3@11, T4@16, T5@19}`，DPT `{A:2,B:4,C:6,D:10,E:12}`；
- Redo：从 LSN 2 起，applied `[4,6,10,12,16,18,19]`，跳过已落盘的 `[2,11]`；
- Undo：loser `[T3,T4,T5]`，写 5 条 CLR 并逐一校验 `undoNextLSN` 链；
- 磁盘终态 `A=150, B=250, C=350, D=400, E=550` —— 已提交数据持久化，
  未提交修改全部回滚；二次恢复幂等（无 loser、无新 CLR）。

## 测试结果

```
ℹ tests 19
ℹ pass 19
ℹ fail 0
```

覆盖：WAL 全类型往返、崩溃前 abort、崩溃于 Undo 中途（CLR undoNextLSN 续传）、
空 WAL / 仅 checkpoint WAL、恢复幂等；路由确定性、空拓扑、单节点、分片均匀性、
副本层级放置、局部性路由、故障转移、最小迁移上界、批量归组无损、
300 并发路由 + 20 次并发再均衡的无泄漏断言。
