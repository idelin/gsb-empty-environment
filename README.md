# ARIES 崩溃恢复引擎（TypeScript）

数据库经典 ARIES 预写日志（WAL）协议的基础协议状态机与核心语义实现：
Analysis 阶段重建 DirtyPageTable / TransactionTable，Redo 阶段重复历史，
Undo 阶段反向扫描并以 CLR（补偿日志记录）补偿 loser 事务。
单节点确定性时钟驱动（LSN 即逻辑时钟），严格遵循状态转换与日志拓扑约束。

## 运行环境

- Node.js >= 23.6（原生 TypeScript 类型擦除，零依赖、零构建步骤）

## 运行样例

```bash
npm run demo     # 构造崩溃场景并执行三阶段恢复，打印各阶段明细
npm test         # Node.js 内置测试运行器，31 个用例
```

`npm run demo` 输出（节选）：

```
=== 崩溃前 WAL ===
LSN  1  UPDATE           txn=1 P1: 0 -> 10
LSN  2  UPDATE           txn=1 P2: 0 -> 20
LSN  3  COMMIT           txn=1
...
LSN  9  BEGIN_CHECKPOINT txn=-
LSN 10  END_CHECKPOINT   txn=- TT=[{"txnId":3,...,"lastLSN":8}] DPT=[["P2",2],["P3",5]]
LSN 11  UPDATE           txn=4 P2: 20 -> 21
LSN 12  UPDATE           txn=5 P3: 30 -> 31
LSN 13  UPDATE           txn=3 P4: 0 -> 40
LSN 14  UPDATE           txn=5 P4: 40 -> 41

=== Analysis ===
TransactionTable: [ [3,{lastLSN:13}], [4,{lastLSN:11}], [5,{lastLSN:14}] ]  (全部 RUNNING)
DirtyPageTable:   [ ['P2',2], ['P3',5], ['P4',13] ]   redoLSN: 2

=== Redo ===
重放 LSN: [ 2, 11, 13, 14 ]  跳过: 3        # 历史重复（含未提交修改）

=== Undo ===
CLR: [ [5,[15,17]], [3,[16,21]], [4,[19]] ]  END 事务: [ 5, 4, 3 ]

=== 恢复后磁盘状态 ===
{ P1: 10, P2: 20, P3: 30, P4: 0 }            # 已提交持久，未提交全部回滚
✔ 恢复正确：已提交数据持久，未提交修改全部回滚
```

## 架构

```
src/
  clock.ts         单节点确定性时钟：LSN 唯一来源，单调无回拨，重启后从 WAL 恢复
  log-record.ts    日志记录类型：UPDATE/CLR/COMMIT/ABORT/END/BEGIN|END_CHECKPOINT
  wal.ts           WAL 管理器：追加（异步互斥串行化）、fsync、单调性校验、索引
  storage.ts       DiskStorage（原子写盘）+ BufferPool（易失缓存、脏页 recLSN）
  txn-manager.ts   运行时一侧：协议状态机、WAL 先行、模糊检查点、ABORT 补偿
  recovery.ts      恢复引擎：analyze / redo / undo / RecoveryEngine
examples/
  scenario.ts      验收场景构造器：2 已提交 + 3 未提交，检查点后崩溃
  demo.ts          三阶段恢复演示
test/              Node.js 内置测试（node:test + assert/strict）
```

## 核心不变量

- **状态机**：`RUNNING --commit--> COMMITTED --end--> ∅`，`RUNNING --abort--> ABORTING --end--> ∅`；非法迁移抛错。
- **WAL 先行**：页修改前先持久化日志；COMMIT 记录强制 fsync 后才算提交点。
- **Analysis**：从最后一个完整 END_CHECKPOINT 种子化 TT/DPT 后前向扫描；`redoLSN = min(DPT.recLSN)`；END 记录将事务从 TT 移除。
- **Redo（历史重复）**：仅当 `page ∈ DPT 且 record.lsn ≥ DPT[page] 且 page.pageLSN < record.lsn` 时重放 after 映像；幂等。
- **Undo（反向补偿）**：所有 loser 按 lastLSN 大根堆全局逆序；每条 UPDATE 先写 CLR（`undoNextLSN = 被撤销记录的 prevLSN`）再改页；CLR 经 `undoNextLSN` 跳过已补偿前缀，崩溃重启绝不重复撤销；事务链耗尽写 END。
- **无泄漏**：恢复结束事务表为空、每个 loser 有 END、WAL 句柄关闭、恢复幂等（再恢复零重放零补偿）。

## 测试覆盖（31 用例，全部通过）

| 文件 | 覆盖点 |
| --- | --- |
| `test/wal.test.ts` | 序列化往返、LSN 连续递增、关闭后追加拒绝、重启时钟恢复、200 路并发追加全序无丢失、非单调 LSN 拒绝、检查点定位 |
| `test/analysis.test.ts` | 检查点种子重建、空 WAL、无检查点全扫、END 移除、recLSN 首次变脏语义、ABORT/CLR 状态保持、孤儿 COMMIT 一致性错误、截断 BEGIN 忽略 |
| `test/redo.test.ts` | 从 min(recLSN) 重复历史、幂等重放、CLR 重做、`lsn < recLSN` 跳过、空 DPT 空转 |
| `test/undo.test.ts` | 全局 LSN 逆序 CLR、undoNextLSN/prevLSN 链、END 终结、恢复中再崩溃不重复撤销、磁盘终态与 pageLSN 精确断言、恢复幂等、空 WAL |
| `test/txn-manager.test.ts` | 非法状态迁移全拒绝、before/after 与 prevLSN 链、运行时 ABORT 补偿、泄漏防护（活跃事务关闭拒绝）、检查点快照、空事务 |
| `test/e2e.test.ts` | 验收场景：3 未提交 + 2 已提交、检查点后崩溃、steal/no-force 现场、三阶段恢复、无悬挂事务、补偿不多不少、崩溃-恢复-再崩溃收敛 |

测试结果：

```
ℹ tests 31
ℹ pass 31
ℹ fail 0
```
