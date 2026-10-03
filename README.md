# ARIES 崩溃恢复引擎 + 海量稀疏数据内存紧凑优化器 (TypeScript)

基于数据库经典 **ARIES 协议** 的预写日志 (WAL) 分析与崩溃恢复 Redo/Undo 引擎，
配套一个面向海量稀疏数据的**自适应内存紧凑容器**（稀疏 Map ↔ 稠密数组双向转换 +
varint/delta/RLE 压缩编解码）。纯 TypeScript、零依赖，使用 Node.js 内置测试运行器验证。

## 环境要求

- Node.js **≥ 23.6**（原生 TypeScript 类型擦除，直接运行 `.ts`）
- 或 Node.js ≥ 22.6，运行时加 `--experimental-strip-types`

## 快速开始

```bash
npm test        # node --test test/*.test.ts （26 个测试）
npm run demo    # 端到端崩溃恢复 + 稀疏优化演示
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/wal.ts` | 预写日志：LSN 分配、缓冲/刷盘、JSONL 持久化、崩溃时丢弃未刷尾部 |
| `src/storage.ts` | 页存储：缓冲池帧 (pageLSN + SparseVector)、脏页刷盘、原子落盘 (tmp+rename) |
| `src/recovery.ts` | ARIES 三阶段：`analyze` / `redo` / `undo`（最大堆选择最大 LSN，CLR 补偿） |
| `src/database.ts` | 事务门面：begin/set/commit/checkpoint/crash/recover，AsyncMutex 串行化临界区 |
| `src/sparse.ts` | 自适应稀疏向量：密度迟滞双阈值转换、maxDenseLength 上限、内存记账 |
| `src/codec.ts` | 压缩编解码：varint、zigzag、索引 delta+连续段 RLE、值 RLE |
| `src/mutex.ts` | AsyncMutex：Promise 链互斥锁，保护 LSN 分配与日志链完整性 |

## ARIES 三阶段

1. **Analysis**：从最近 `end_checkpoint` 快照播种 TransactionTable / DirtyPageTable，
   向前扫描至日志尾，精确重建崩溃瞬间的 TT（含 lastLSN/status）与 DPT（pageId→recLSN）。
2. **Redo（重复历史）**：从 `min(recLSN)` 起扫描，对 DPT 内页面且 `pageLSN < lsn` 的
   update/CLR 记录重做 after-image——已提交与未提交的修改一律重放。
3. **Undo（逆序补偿）**：以最大堆按 LSN 降序处理 loser 事务的 `prevLSN` 链；
   每条 update 写入 CLR（`undoNextLSN` 指向被补偿记录的 `prevLSN`）并回滚页面，
   链到达 begin 时写 `end`。恢复幂等：二次恢复不产生新 CLR。

## 稀疏数据内存紧凑优化

- **自适应容器**：密度 ≥ 0.6 且长度 ≤ `maxDenseLength` 时转稠密数组；密度 < 0.25
  或越界时回落稀疏 Map（迟滞区间防抖动）。
- **压缩**：索引 delta 编码 + 连续段 RLE（≥3 连续合并为 2 个 varint），值 zigzag+RLE；
  1 万个同值连续元素序列化后 < 64 字节。
- **内存记账**：`MemoryAccountant` 精确跟踪每次转换/清理的字节数，测试断言严格归零。

## 验收场景（test/recovery.test.ts）

WAL 包含 **2 个已提交事务（T1 于 Checkpoint 前、T2 于其后）+ 3 个未提交事务
（T3/T4/T5）**，Checkpoint 之后崩溃。恢复后断言：

- Analysis：TT 恰好含 T3/T4/T5（running），DPT = `{P1:2, P2:3, P3:14}`
- Redo：重放 LSN `[9,13,14,16,18,19]`，未提交修改也按历史重现
- Undo：写入 5 条 CLR，T3/T4/T5 全部回滚并写 `end`
- 最终落盘：`P1[5]=150, P2[7]=200, P2[9]=0, P3[1]=0, P3[2]=0`
- 幂等：二次恢复 0 条新 CLR、0 条重放
- 无泄漏：事务表/脏页表/WAL 缓冲/内存记账全部归零

## 运行样例（npm run demo 摘要）

```
[2] Analysis 阶段
  事务 T3: status=running, lastLSN=14
  事务 T4: status=running, lastLSN=16
  事务 T5: status=running, lastLSN=19
  脏页 P1: recLSN=2 / P2: recLSN=3 / P3: recLSN=14
[3] Redo 阶段 (重复历史)
  重放 LSN: [9, 13, 14, 16, 18, 19]
[4] Undo 阶段 (逆序补偿)
  写入 5 条 CLR: [20, 21, 23, 25, 26]，回滚事务: [T3, T4, T5]
[5] 恢复后最终状态: P1[5]=150  P2[7]=200  P2[9]=0  P3[1]=0  P3[2]=0
```

## 测试结果（npm test）

```
ℹ tests 26
ℹ pass 26
ℹ fail 0
```

覆盖：ARIES 三阶段与幂等性、Checkpoint 快照播种、稀疏容器全部边界
（空/单元素/巨大索引/稠密化/稀疏化/迟滞/maxDenseLength/损坏数据拒绝）、
50 万元素 × 1 亿索引空间海量负载、64 并发工作者互斥精确计数、
16 并发事务 WAL 链完整性、并发崩溃恢复，以及全链路无泄漏断言。
