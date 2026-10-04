# ARIES WAL 崩溃恢复引擎（TypeScript）

数据库经典 ARIES 协议的 TypeScript 实现：预写日志（WAL）+ 三阶段崩溃恢复
（Analysis / Redo / Undo），紧凑二进制编解码与零拷贝还原。

## 结构

- `src/codec.ts` — LEB128 varint、CRC32、边界检查的 Reader/Writer；`readBytes`
  返回共享内存视图（零拷贝），越界/截断/溢出抛 `CodecError`。
- `src/logrecord.ts` — 7 种日志记录的版本化序列化。v1→v2 向后兼容（缺省字段
  补默认值），帧定长前缀使读取方可跳过未知尾部字段（向前兼容）。
- `src/wal.ts` — `[len][crc32][payload]` 帧化日志，LSN = 帧在文件中的字节偏移；
  异步互斥链保证并发追加的 LSN 唯一且帧不交错；打开时截断撕裂写尾部；
  扫描惰性解析，记录字节字段零拷贝引用文件映像。
- `src/storage.ts` — 页存储（4KB 页，页头 8 字节 pageLSN）与缓冲池
  （steal 驱逐 / no-force 提交）。
- `src/recovery.ts` — ARIES 三阶段：
  - **Analysis**：从最近 Checkpoint 重建事务表（ATT）与脏页表（DPT），
    得出 redoLSN。
  - **Redo**：历史重复，从最小 recLSN 重放 UPDATE/CLR 后像，
    以 pageLSN 幂等跳过。
  - **Undo**：按 LSN 大顶堆反向扫描 loser 事务，每个被撤销的 UPDATE 写
    CLR（含 undoNextLSN），事务链走到 0 时写 END。
- `src/database.ts` — 正常处理路径门面（begin/update/commit/checkpoint/
  stealPage/crash），用于构造崩溃场景。

## 运行

```bash
npm test    # 编译 + Node 内置测试（node:test）
npm run demo  # 端到端崩溃恢复演示
```

环境说明：仓库内 vendored TypeScript 5.9.3（`tools/typescript/`），
测试需 Node ≥ 18（脚本使用 `/opt/homebrew/bin/node`，v25）。

## 验收场景

`test/recovery.test.ts` 构造：T1 提交并 END、T2 提交未 END、T3/T4/T5 未提交；
中间一次 fuzzy checkpoint；崩溃前 steal 两页脏页（含未提交数据）落盘。
恢复后断言：已提交数据全部持久化、未提交修改全部回滚（含已落盘的）、
4 条 CLR、3 条 ABORT、5 条 END、ATT 为空、缓冲池无脏页、资源全部关闭；
二次恢复幂等（不产生新 CLR）。
