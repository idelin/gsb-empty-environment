# ARIES 崩溃恢复引擎 + 增量快照/检查点归档 (TypeScript)

数据库经典 **ARIES** 协议的零依赖 TypeScript 实现：WAL 预写日志、三阶段崩溃恢复
（Analysis / Redo / Undo + CLR），以及低停顿的**增量快照**与**检查点归档**子系统。

## 运行环境

- Node.js **>= 23.6**（原生 TypeScript 类型擦除 + 内置 `node:test`，无需任何依赖、无需编译）
- 仓库内置 `.nvmrc`（v23.9.0）：`nvm use` 后即可

```bash
nvm use            # 或任意 >= 23.6 的 Node
npm test           # 运行全部测试（node --test）
npm run demo       # 运行端到端演示（崩溃 + 恢复 + 快照归档）
```

如需静态类型检查（可选）：`npm i -D typescript @types/node && npx tsc --noEmit`。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/types.ts` | LSN、日志记录类型（BEGIN/UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT）、TT/DPT |
| `src/wal.ts` | 预写日志：内存缓冲 + `flush()` 强制落盘（JSON Lines），重启后续接 LSN |
| `src/storage.ts` | 页式磁盘存储（原子 tmp+rename）+ 缓冲池（steal/no-force、recLSN 追踪、COW 钩子） |
| `src/txn.ts` | 事务管理：BEGIN/UPDATE/COMMIT（强制日志）/ABORT（在线 CLR 回滚）/END |
| `src/checkpoint.ts` | 模糊检查点：BEGIN/END_CHECKPOINT 对，END 携带事务表与脏页表 |
| `src/recovery.ts` | ARIES 三阶段恢复管理器（见下） |
| `src/snapshot.ts` | 增量快照、归档、恢复、保留策略（见下） |

## ARIES 三阶段恢复

1. **Analysis** — 反向定位最近的 `END_CHECKPOINT`，以其 TT/DPT 为起点前向扫描到日志尾，
   重建事务表（RUNNING/COMMITTING/ABORTING）与脏页表（pageId → recLSN），得出 redoLSN。
2. **Redo（历史重复）** — 从 redoLSN 前向扫描，对 DPT 中 `recLSN <= lsn` 且磁盘页
   `pageLSN < lsn` 的 UPDATE/CLR 重放后像，包括未提交事务的修改。
3. **Undo（反向扫描 + CLR）** — 对所有败者事务按全局最大 LSN 优先逐条回滚，
   每撤销一条 UPDATE 写入一条 CLR（携带 `undoNextLSN`），事务清零后写 END。
   COMMITTING 状态事务（提交记录已落盘、END 丢失）直接补 END 而不回滚。
   恢复过程可崩溃重入：CLR 本身参与 Redo，重复恢复幂等。

## 增量快照与检查点归档（低停顿设计）

- **Copy-on-Write**：`beginSnapshot` 仅记录 WAL 的 `baseLSN` 并注册缓冲池写前钩子，
  不复制任何数据；快照期间被修改的页在首次写入时暂存修改前映像，写事务零阻塞。
- **增量物化**：`completeSnapshot` 只为 `pageLSN` 相较父快照发生变化的页写新文件，
  未变化的页在 manifest 中直接引用旧快照文件（零拷贝）。
- **一致性点**：快照恰好反映 `baseLSN` 时刻的状态；`baseLSN` 之后新建的页自动排除。
- **归档**：`archiveSnapshot` 把快照固化为自包含 bundle（`archive/snapshot-<id>.json`），
  并把其他快照共享引用的页文件 re-home 后再删除原目录，依赖链不断裂。
- **保留策略**：`prune(keepLast)` 自动归档最旧快照；`restore` 对活动快照与归档透明。
- **并发安全**：快照生命周期（begin→complete）与归档/修剪通过 FIFO 互斥锁串行化，
  并发快照自动排队，互不干扰。

## 验收场景（`test/recovery.test.ts` 第一条 + `examples/demo.ts`）

构造 3 个未提交事务（T3/T4/T5）+ 2 个已提交事务（T1/T2），中间做一次 Checkpoint，
随后崩溃（含未提交数据的脏页已被 steal 到磁盘，其余缓冲页丢失）。恢复后断言：

- Analysis：TT = {T3,T4,T5}（RUNNING），DPT 精确到每页 recLSN，redoLSN 正确；
- Redo：丢失的已提交页被重放，已在磁盘上的页按 pageLSN 跳过；
- Undo：4 条 CLR 精确回滚 T3/T4/T5，最终 `p1=A1, p2=B1x, p3=C2, p4='', p5=''`；
- 无泄漏：WAL 缓冲清空、脏页表清空、无临时文件、无活动事务。

## 测试（23 个用例，`node:test` + `assert/strict`）

- `test/recovery.test.ts` — 验收场景、无检查点分析、pageLSN 守卫、Undo 中崩溃可重启幂等、
  COMMITTING 边界、崩溃前 Abort。
- `test/checkpoint.test.ts` — 检查点 TT/DPT 内容、多检查点取最新、空检查点、检查点强制 WAL。
- `test/snapshot.test.ts` — 全量/增量/零增量/空库快照、COW 一致性、异步并发写入下的
  时间点一致性、并发快照串行化、归档自包含与依赖修复、prune 保留、归档回退恢复、
  ID 单调、中止快照、崩溃-恢复-归档全链路，以及严格的泄漏断言。
