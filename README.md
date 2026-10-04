# ARIES 崩溃恢复引擎 (TypeScript)

数据库经典 ARIES 预写日志 (WAL) 分析与崩溃恢复协议的 TypeScript 实现,
包含三阶段恢复 (Analysis / Redo / Undo)、CLR 反向补偿与事务回滚状态编排。
零依赖, 仅使用 Node.js (>=23.6) 内置的类型擦除能力直接运行 TypeScript。

## 运行

```bash
npm run demo    # 运行崩溃-恢复演示 (3 未提交 + 2 已提交事务, checkpoint 后崩溃)
npm test        # 运行全部 21 个 node:test 测试
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/log-record.ts` | 日志记录类型: BEGIN/UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT, LSN 链 |
| `src/wal.ts` | 预写日志: 追加 (互斥)、按 LSN 刷盘、崩溃后仅持久前缀可见 |
| `src/storage.ts` | 磁盘存储 (原子写) + 缓冲池 (pageLSN 幂等语义) |
| `src/txn-manager.ts` | 崩溃前事务管理器: 产生 WAL 负载, 提交时强制 WAL 刷盘 |
| `src/recovery.ts` | ARIES 三阶段恢复引擎 (核心) |
| `src/scenario.ts` | 验收场景构造: 5 事务 + fuzzy checkpoint + 选择性刷盘后崩溃 |

## 三阶段恢复

1. **Analysis** — 从最近的 checkpoint 前向扫描, 重建 TransactionTable
   (RUNNING/COMMITTED/ABORTING) 与 DirtyPageTable (pageId → recLSN)。
2. **Redo** — 从 DPT 最小 recLSN 开始重复历史 (含 CLR), 以
   `pageLSN >= lsn` 跳过已持久化的更新, 保证幂等。
3. **Undo** — 对 loser 事务按全局 LSN 逆序回滚: 每个 UPDATE 写一条 CLR
   (`undoNextLSN = prevLSN`) 并立即应用前像; 遇到 CLR 直接跳转
   `undoNextLSN`, 跳过崩溃前已补偿的更新 (不重复补偿); 链到达 BEGIN
   或 NULL_LSN 时写 END 收尾。

## 失败逆向补偿与幂等

- CLR 本身就是日志: 恢复过程中再次崩溃, 重启后 Redo 会重放 CLR,
  Undo 通过 `undoNextLSN` 继续而非重来 — 补偿永不重复执行。
- `recover()` 整体由 FIFO 互斥锁串行化; 并发调用排队执行, 首次之后的
  每次运行经 LSN 检查验证为完全无操作 (0 CLR / 0 新增日志)。
- 无泄漏断言: 恢复后事务表为空、缓冲池无脏页、互斥锁全部释放。

## 测试覆盖 (21 个用例)

- `test/recovery.test.ts` — 验收场景端到端、CLR 补偿链、Redo/恢复幂等、无泄漏
- `test/edge-cases.test.ts` — 空 WAL、仅 checkpoint、单更新 loser、
  无 END 的 winner、崩溃时 ABORTING、undoNextLSN=0 终止、多 loser 逆序、
  pageLSN 跳过、recLSN 起点、checkpoint 快照合并、CLR 的 Redo 重放
- `test/concurrency.test.ts` — 并发 recover 串行化且总量恰好一次、
  WAL append 的 LSN 单调无空洞、互斥锁异常释放与 FIFO 公平性
