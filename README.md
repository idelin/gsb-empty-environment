# ARIES 崩溃恢复引擎 + 确定性事件驱动模拟器 (TypeScript)

数据库经典 **ARIES** 预写日志（WAL）崩溃恢复协议的端到端实现：三阶段恢复
（Analysis / Redo / Undo + CLR 补偿日志）、steal/no-force 缓冲池、模糊检查点
（fuzzy checkpoint）、严格两阶段锁（strict 2PL），以及一个离散时间、事件驱动、
种子确定的故障注入模拟框架，支持百万步回归测试与 BUG 的逐比特复现。

纯 TypeScript（仅使用可擦除类型语法），零第三方依赖，直接由 Node.js ≥ 22.6
的原生类型剥离（type stripping）运行，测试使用 Node.js 内置测试框架 `node:test`。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/types.ts` | 日志记录类型：UPDATE / CLR / COMMIT / ABORT / END / BEGIN·END_CHECKPOINT |
| `src/wal.ts` | `LogManager`（易失尾部 + flush 持久化）、内存/JSONL 文件两种 WAL 存储 |
| `src/storage.ts` | `Disk`（持久）与 `BufferPool`（易失，LRU，steal/no-force，刷脏页前强制 WAL 协议） |
| `src/lock.ts` | 严格 2PL（page,key 粒度排他锁，持有至 commit/abort，冲突即失败 ⇒ 无死锁） |
| `src/txn.ts` | 事务管理器：先记日志后改页、commit 刷日志、abort 写 CLR、模糊检查点 |
| `src/recovery.ts` | **ARIES 三阶段恢复引擎**（见下） |
| `src/verify.ts` | 恢复后严格校验：重放已提交事务推导期望磁盘态、CLR/END 完备性、prevLSN 链完整性 |
| `src/simulator.ts` | 离散时间事件队列（二叉堆）+ 种子化 PRNG + 崩溃注入 + 恢复 + 无泄漏断言 |
| `src/scenario.ts` | 验收场景构造器：2 已提交 + 3 未提交事务、检查点后崩溃的 WAL 文件 |
| `src/demo.ts` | CLI：验收场景演示 / 模拟 / 故障复现 |

## ARIES 三阶段恢复

1. **Analysis** — 从最近一次完成的模糊检查点（END_CHECKPOINT 快照）开始前扫，
   重建 TransactionTable 与 DirtyPageTable，确定 `redoLSN = min(recLSN)`；
   扫描末仍在 TT 中的事务即 loser（COMMITTED 无 END 者为 winner）。
2. **Redo（历史重复）** — 从 `redoLSN` 前扫，对 DPT 中页面的每条 UPDATE/CLR，
   仅当磁盘页 `pageLSN < lsn` 时重放 after-image，已持久化的跳过。
3. **Undo（补偿日志）** — 按全局 LSN 降序回滚所有 loser：每条 UPDATE 写一条
   CLR（`undoNextLSN` 指向前驱，崩溃前已补偿的不再重做），事务链走到头写 END。
   CLR 自身永不被撤销，保证幂等与可重入。

## 运行

需要 Node.js ≥ 22.6（建议 23.6+，无需任何标志；22.6–22.x 需
`node --experimental-strip-types`）。仓库内 `.nvmrc` 固定 23.9.0。

```bash
npm test                 # node --test "test/*.test.ts"
npm run demo             # 验收场景：构造 WAL 文件 -> 崩溃 -> 三阶段恢复 -> 校验
node src/demo.ts simulate 42 1000000        # 确定性模拟：seed=42，1M 事件
node src/demo.ts reproduce 99 50000 15000   # 故障复现：seed=99，第 15000 事件处崩溃
```

## 运行样例

`npm run demo`（截选）：

```
Txns: T1, T2 committed; T3, T4, T5 uncommitted
Checkpoint taken with T3,T4 active; P1 flushed dirty (steal); crash.

--- WAL after recovery (20 records) ---
  LSN= 10 BEGIN_CHECKPOINT
  LSN= 11 END_CHECKPOINT    begin=10 TT=[T3:RUNNING, T4:RUNNING] DPT={P3@4, P1@0, P2@1, P4@9}
  LSN= 12 UPDATE            T5 P3.k1 30->31
  LSN= 13 CLR               T5 P3.k1 31->30 undoNext=null
  ...
--- Analysis ---
  TransactionTable: T3:RUNNING@7, T4:RUNNING@9, T5:RUNNING@12
  DirtyPageTable:   P3@4, P1@0, P2@1, P4@9
--- Undo ---
  losers=[T3, T4, T5] winners=[]  CLRs written=4 ENDs written=3
--- Final disk state ---
  P1: {"k1":10}  P2: {"k1":20}  P3: {"k1":30}  P4: {"k1":0}
  RESULT: PASS - committed data durable, uncommitted data rolled back.
```

`node src/demo.ts simulate 42 50000`：

```
seed=42 events=50000 crashAt=50000
walLength=51167 walHash=f53301091b6f7ba9 diskHash=30b88cb3...
committed=6038 aborted=2648 losers=[8686,8687,8689,8690]
redo applied=18 skipped=24 undo CLRs=1
verification: PASS (512 keys checked)
```

## 测试结果

`npm test`：22 个测试全部通过（Node v23.9.0）：

```
✔ analysis: reconstructs TT and DPT from fuzzy checkpoint
✔ analysis: works without any checkpoint (scans from LSN 0)
✔ redo: repeats history for committed txn whose pages were never flushed (no-force)
✔ redo: skips updates already on disk (pageLSN >= lsn)
✔ undo: writes CLR chain with undoNextLSN and END for each loser
✔ undo: resumes partial abort via undoNextLSN without re-undoing (crash after CLRs, END lost)
✔ recovery: empty WAL is a no-op
✔ recovery: committed-only WAL needs no undo
✔ recovery: loser with no log records still gets END (leak-free)
✔ recovery: committed txn with lost END record is a winner, not a loser
✔ event queue: pops in deterministic (time, seq) order
✔ wal file format: every record is one JSON line with monotonic LSNs
✔ end-to-end: checkpointed WAL with 2 committed + 3 uncommitted txns recovers correctly
✔ end-to-end: WAL file is valid JSON lines, human-inspectable
✔ determinism: same seed produces bit-identical WAL and disk state
✔ determinism: different seeds diverge
✔ fault reproduction: (seed, events, crashAt) replays the exact same crash and recovery
✔ crash window: every crash point in a sweep recovers cleanly
✔ contention: 32 concurrent txns on 4 pages x 2 keys stays correct (strict 2PL)
✔ durability: committed txn survives crash even when its pages were never flushed
✔ steal: uncommitted data flushed to disk before crash is rolled back
✔ million-step regression: 1,000,000 deterministic events, golden hashes (≈16s)
ℹ tests 22  ℹ pass 22  ℹ fail 0
```

百万步黄金哈希（seed=7）：`walHash=6e3cad08f8f3e9bf`，
`diskHash=f0eb6c202a3ddcaf1d10adc01b2dd79e2917526a32b486edf0994ccf4e9f0bfa`。

## 模拟器发现并修复的真实 BUG

确定性故障复现框架在 seed=42 / 20000 事件处暴露了一个 WAL 协议违例：
缓冲池 steal 刷脏页时未先将日志刷至该页 `pageLSN`，导致磁盘页携带
“日志未持久化”的修改，崩溃后该修改无法撤销（磁盘出现 WAL 中不存在的值）。
修复：`BufferPool.flushPage` 在写盘前强制 `log.flush(page.pageLSN)`。
该场景由 `crash window` 扫描测试与百万步回归看守住。

## 无泄漏断言

每次模拟结束强制检查：缓冲池全部刷净、日志尾部清空、事件队列排空、
每个 loser 事务的 CLR 数 == UPDATE 数且以 END 收尾、prevLSN 链完整、
磁盘态与“已提交事务按 LSN 序重放”的期望态逐键一致。
