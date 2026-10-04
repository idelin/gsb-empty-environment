# page-txn-log — 本地页事务日志（ARIES 风格教学实现）

TypeScript 实现，零依赖，直接由 Node ≥ 22.6（原生 type stripping）运行。

## 运行

```bash
# 需要 Node >= 22.6（建议 23.x），例如：
export PATH="$HOME/.nvm/versions/node/v23.9.0/bin:$PATH"

npm test                          # node:test 全量测试（17 个）
node src/cli.ts run <scenario>    # 运行场景并输出 JSON 结果
node src/cli.ts plan --dir D      # 打印日志保留边界及理由
node src/cli.ts truncate --dir D [lsn]   # 回收日志段（越界拒绝，退出码 2）
node src/cli.ts dump|pages --dir D       # 查看保留日志 / 已提交页版本
```

场景：`nested-savepoints`、`interleaved`、`crash-mid-rollback`、`torn-checkpoint`、`truncate-verify`。

## 设计要点

- **日志记录**：`UPDATE` 带 before/after 映像与 `prevLSN` 链；`CLR` 带 `undoNextLSN`
  与 `undidLSN`；`SAVEPOINT` 把名字绑定到事务内 LSN（重名覆盖旧绑定）；
  `BEGIN/COMMIT/ABORT/END`；checkpoint 为 `CKPT_BEGIN`+`CKPT_END` 成对出现才算完整。
- **rollbackTo**：从事务 `lastLSN` 沿 `prevLSN` 回走，只补偿保存点之后、尚未补偿的
  UPDATE（遇到 CLR 跳 `undoNextLSN`），每个被撤销的更新生成一条 CLR；重复回滚幂等；
  回滚后事务可继续写并提交；整体 abort 复用同一逻辑，天然跳过已有 CLR。
- **锁与可见性**：严格事务级独占锁（持有到 commit/abort，冲突即抛
  `LockConflictError`）；外部读者走 committed view，只能看到已提交版本。
- **恢复**：analysis 从最后一个完整 checkpoint 开始（半写 checkpoint 因帧 CRC/长度
  校验失败被忽略，自动回退）；redo 从脏页表最小 recLSN 重放历史；undo 只处理
  loser 事务且 CLR-aware，绝不重复补偿。
- **日志回收**：保留边界 = min(活动事务 firstLSN, 脏页 recLSN, 最后完整
  checkpoint 的 beginLSN)；无完整 checkpoint 时保守拒绝回收；请求越过边界抛
  `TruncationRefusedError` 并输出理由。
- **合成文件层**（`src/fileLayer.ts`）：记录帧 `[len][payload][crc32]`，段文件滚动；
  故障注入：`armCrashAfterAppends(n)`（回滚中途重启）、`armTornNextAppend()`
  （checkpoint 半写）；截断请求越界即拒绝。
- **正确性交叉验证**：`src/rebuild.ts` 从完整日志独立重建最终页（只重放已提交事务的
  after 映像），与截断后恢复结果逐页比较（`truncate-verify` 场景与测试）。

## 文件

- `src/types.ts` — LSN / 记录类型
- `src/fileLayer.ts` — 合成持久文件层 + 故障注入（CRC 帧、段、崩溃/撕裂写）
- `src/logManager.ts` — LSN 分配、段滚动、完整 checkpoint 识别、段截断
- `src/pageStore.ts` — 缓冲池（pageLSN/recLSN/dirty）、页文件、锁管理器、committed view
- `src/txnManager.ts` — begin/update/savepoint/rollbackTo/commit/abort
- `src/recovery.ts` — analysis / redo / undo
- `src/rebuild.ts` — 从完整日志独立重建最终页
- `src/system.ts` — 装配、checkpoint、截断规划与拒绝
- `src/scenarios.ts` / `src/cli.ts` — 场景与命令行
- `tests/` — 保存点、锁、恢复、截断四组 node:test 测试
