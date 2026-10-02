# ARIES 崩溃恢复引擎 + 稀疏数据内存紧凑优化器（TypeScript）

数据库经典 ARIES 预写日志崩溃恢复协议，配合面向海量稀疏数据的自适应内存紧凑页存储。
纯 Node.js 内置模块实现（`node:test` / `node:assert` / `node:fs`），零依赖，
利用 Node ≥ 23 的原生 TypeScript 类型擦除直接运行 `.ts`。

## 运行

```bash
npm test     # Node 内置测试运行器，17 个测试
npm run demo # 运行样例：构造崩溃场景 -> 从 WAL 文件恢复 -> 打印三阶段结果
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/log.ts` | WAL：UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT 记录，LSN 分配，JSON Lines 文件持久化 |
| `src/sparse.ts` | SparsePage：sparse(Map)⇄dense(Float64Array) 自适应转换（内存核算 + 滞后带）、RLE 压缩、AsyncLatch 并发闩锁、AllocationTracker 泄漏追踪 |
| `src/storage.ts` | DiskManager（RLE 页镜像）+ BufferPool（steal/no-force，运行时脏页表） |
| `src/txn.ts` | 事务管理器：先写日志、prevLSN 链、commit/abort/checkpoint |
| `src/recovery.ts` | ARIES 三阶段：Analysis（重建 TT/DPT）→ Redo（历史重复）→ Undo（反向扫描 + CLR） |
| `src/scenario.ts` | 验收场景：2 已提交 + 3 未提交 + 1 abort 半途事务，检查点后崩溃 |

## ARIES 三阶段

1. **Analysis**：从最近 CHECKPOINT 正向扫描，重建 TransactionTable（状态/lastLSN）
   与 DirtyPageTable（pageId→recLSN），确定 redo 起点 = min(recLSN)。
2. **Redo**：历史重复。重放全部 UPDATE/CLR（含未提交事务与补偿记录），
   仅当 `DPT[page] ≤ lsn` 且磁盘 `pageLSN < lsn` 时应用，幂等。
3. **Undo**：败者事务（RUNNING/ABORTING）按 LSN 降序反向回滚，每步写 CLR
   （`undoNextLSN` 指向下一待撤销记录，跳过已补偿部分），链尾写 END。

## 稀疏数据优化器

- **自适应容器**：密度低用 `Map`（每非默认槽位 32B），密度高用 `Float64Array`
  （固定 32KB）；转换由内存核算驱动，10% 滞后带防止阈值抖动。
- **RLE 压缩**：页序列化为行程块，全默认页仅 1 块，交替最坏情况 4096 块。
- **并发安全**：`AsyncLatch` 保护 read-modify-write 临界区；
  对照测试证明无闩锁时协作式调度下丢失更新。
- **无泄漏**：`AllocationTracker` 计数存活页对象，`dispose()` 幂等释放，
  测试断言基线归零。
