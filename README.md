# aries-wal

基于 WAL（预写日志）的单机事务持久化与 ARIES 恢复管理器，TypeScript 实现，
零依赖，使用 Node.js 内置测试运行器验证（Node >= 22.6，原生类型擦除直接运行 `.ts`）。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/log.ts` | WAL 日志管理器：追加型日志文件（JSONL），`append`/`flush`（fsync）/扫描/检查点定位 |
| `src/disk.ts` | 页式磁盘存储：4KB 定长页，页头 8 字节持久化 `pageLSN` |
| `src/buffer.ts` | 缓冲池：写前日志（`flushPage` 先 `log.flush(pageLSN)` 再落盘），脏页跟踪 |
| `src/database.ts` | 事务门面：`begin/write/commit/abort/checkpoint`，维护事务表与脏页表（DPT） |
| `src/recovery.ts` | ARIES 三阶段恢复：Analysis → Redo → Undo，CLR 补偿日志 |

## 核心机制

- **先日志后数据**：每次 `write` 先追加 `update` 日志（含 before/after 镜像）再改内存页；
  提交时强制 `fsync` 日志；刷脏页前保证对应日志已持久化。
- **事务表 / 脏页表**：运行期维护 `txnId -> {status, lastLSN}` 与 `pageId -> recLSN`，
  检查点将二者快照写入日志。
- **崩溃恢复**（`Database.open()` 自动执行）：
  1. **Analysis**：从最近检查点正向扫描，重建事务表与 DPT，区分 winners/losers；
  2. **Redo**：从 DPT 最小 `recLSN` 起重放所有 `update`/`clr` 的 after 镜像，
     通过页上 `pageLSN` 跳过已持久化记录（重复历史，幂等）；
  3. **Undo**：按 `lastLSN` 逆序回滚所有 loser 事务，每个撤销动作写 CLR
     （含 `undoNextLSN`），崩溃于 Undo 中途也不会重复回滚。
- 日志记录类型：`begin / update / commit / abort / end / clr / checkpoint`。

> 说明：本实现聚焦 WAL 与恢复协议，不含并发控制；假定同一数据项不会同时被
> 多个未提交事务修改（真实系统中由 2PL 保证）。

## 运行

```bash
npm test     # node --test：8 个崩溃恢复测试
npm run demo # 批量更新中途断电 → 重启恢复的完整演示
```

## 演示样例（`npm run demo` 实际输出）

```
[phase 2] batch of 5 transfers on disjoint accounts; commit #1/#3/#5, leave #2/#4 open
  txn 2: move 10 from acct 0 to acct 1 -> COMMITTED
  txn 3: move 20 from acct 2 to acct 3 -> still open
  ...
[phase 3] *** POWER LOSS *** dirty buffer pages are gone, WAL survives
[phase 4] reboot: Database.open() runs ARIES recovery
  analysis: winners=[] losers=[3,5] dpt size=1
  redo:     startLSN=2 applied=10 skipped=10
  undo:     txns=[5,3] clr written=4
[phase 5] verification
  committed transfers applied, uncommitted rolled back: OK
  total preserved (no corruption): OK
```

## 测试覆盖（`test/recovery.test.ts`）

- 已提交数据在干净关停后存活
- Redo：已提交但未刷盘的脏页在崩溃后重放恢复
- Undo：未提交事务在崩溃后回滚到 before 镜像
- 批量更新中途断电：已提交保留、未提交回滚、未触及槽位无损坏
- 恢复幂等：二次恢复字节完全一致
- 运行期 `abort()` 通过 CLR 回滚
- 检查点与在途事务并存时的恢复正确性
- 恢复后事务 ID 不发生碰撞
