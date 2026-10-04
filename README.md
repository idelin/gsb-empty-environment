# ARIES 崩溃恢复引擎 + 形式化约束巡检仪（TypeScript）

数据库经典 ARIES 协议的完整实现：预写日志（WAL）、三阶段崩溃恢复
（Analysis / Redo / Undo + CLR 补偿），以及全量状态离线完整性巡检仪
（悬挂引用、孤立环路、数据倾斜检测与不变式断言）。

纯 TypeScript、零依赖：利用 Node.js ≥ 22.6 的原生类型擦除直接运行，
测试使用 Node 内置 `node:test` 运行器。

## 运行

```bash
# 需要 Node.js >= 22.6（推荐 >= 23.6，类型擦除默认开启）
node src/demo.ts    # 运行验收场景演示
node --test         # 运行全部 28 个测试
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/types.ts` | 日志记录类型：UPDATE / CLR / COMMIT / ABORT / END / CHECKPOINT |
| `src/wal.ts` | WAL 文件（JSON Lines），promise 链串行化追加，保证并发下 LSN 全局有序 |
| `src/storage.ts` | 模拟磁盘（pageLSN）与缓冲池（脏页 recLSN 跟踪） |
| `src/txn-manager.ts` | 前向处理：事务更新/提交/中止/检查点，每事务操作链串行化 |
| `src/recovery.ts` | 三阶段恢复引擎：Analysis 重建 TT/DPT，Redo 历史重复，Undo 反向补偿 |
| `src/inspector.ts` | 形式化约束巡检与不变式断言仪（5 类检查） |
| `src/scenario.ts` | 验收场景：3 未提交 + 2 已提交事务，Checkpoint 后崩溃 |
| `src/demo.ts` | 端到端演示 |

## 恢复三阶段

1. **Analysis**：从最近 END_CHECKPOINT 快照出发向前扫描，重建崩溃瞬间的
   TransactionTable（含 running/committed/aborting 状态）与 DirtyPageTable。
2. **Redo（历史重复）**：从最小 recLSN 起扫描，仅当 `pageLSN < log.lsn` 时
   重放 after-image —— 幂等，可重复执行。
3. **Undo（反向扫描）**：以最大 LSN 优先的全局逆序回滚所有败者事务，
   每撤销一条 UPDATE 写入 CLR（`undoNextLSN` 指向下一个待撤销记录），
   保证恢复过程本身崩溃后仍可重启续作；已提交事务补写 END。

## 巡检仪检查项

- **WAL 结构不变式**：LSN 连续递增、prevLSN 链完整、CLR.undoNextLSN 合法、
  事务闭合性（END 之后无孤立记录、无悬挂事务）。
- **悬挂引用**：日志→页面、检查点快照→日志、分析结果（TT/DPT）→日志的指针有效性。
- **孤立环路**：prevLSN/undoNextLSN 链接图必须为 DAG（三色 DFS 环检测）。
- **数据倾斜**：页面访问分布的热点占比与 z-score 双重判定（可配阈值）。
- **恢复后不变式**：事务表为空（无泄漏）、磁盘 pageLSN 覆盖全部脏页（无丢失更新）。

## 运行样例（`node src/demo.ts`）

```
[3] 三阶段恢复
    Analysis: TT = T1(running,last=14), T3(running,last=10), T5(running,last=11)
             DPT = A:1, C:3, D:4, E:9, B:10
    Redo: 重放 5 条记录（历史重复）
    Undo: 回滚败者事务 [T1, T3, T5]，写入 6 条 CLR、3 条 END

[4] 恢复后磁盘状态: { A: 0, B: 20, C: 0, D: 0, E: 50 }
    期望值      : { A: 0, B: 20, C: 0, D: 0, E: 50 }

[5] 形式化约束巡检（恢复后全量状态）
    通过 11 项检查，违规 0 项，告警 0 项
    无悬挂引用 / 无孤立环路 / 无事务泄漏
```

## 测试结果（`node --test`，Node v23.9.0）

```
ℹ tests 28
ℹ pass 28
ℹ fail 0
```

覆盖：验收场景端到端恢复、Redo 幂等/跳过、Undo 可重启（abort 中途崩溃）、
二次恢复、空 WAL/无检查点边界、巡检仪全部边界条件（LSN 断裂、链断裂、
悬挂页面/undoNextLSN/检查点快照/分析结果引用、孤立环路、孤立记录、
悬挂事务、数据倾斜阈值边界、空 WAL）、并发安全（200 并发追加 LSN 唯一连续、
同事务并发更新链完整、检查点原子性、并发事务崩溃恢复一致性）与无泄漏断言。
