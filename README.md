# VersionSet — 崩溃一致性 LSM 元数据版本管理器

以 TypeScript 实现的 LSM 树元数据持久化与原子版本演进器。记录每一层当前的
SSTable 文件列表与校验和；Compaction 通过追加 VersionEdit 日志原子切换
VersionSet，崩溃重启后准确恢复最新目录结构。

## 设计

- **MANIFEST**：append-only 的 VersionEdit 日志。每条记录按
  `[len u32][crc32 u32][payload(JSON)]` 帧格式编码，写入后立即 `fsync`，
  保证单条记录的原子可见性；尾部撕裂记录（崩溃残留）在恢复时被 CRC/长度
  校验识别并截断。
- **CURRENT**：指向活跃 MANIFEST 文件，通过 tmp + rename + 目录 fsync 原子更新。
- **VersionEdit**：记录新增文件（层级、文件号、大小、key 范围、CRC32 校验和）
  与删除文件，一次 `logAndApply` = 追加一条日志 + fsync + 应用到内存状态，
  构成原子版本切换。
- **Compaction 两阶段**：先把合并结果写成新的 SSTable 文件并 fsync
  （此时旧版本仍是唯一有效版本），再 `logAndApply` 提交 VersionEdit。
  崩溃发生在提交前 → 重启后恢复出旧版本，未被任何版本引用的孤儿 SSTable
  由 `removeObsoleteFiles()` 扫描清除。
- **恢复时校验**：回放 MANIFEST 重建各层文件列表后，逐个重算存活 SSTable
  的 CRC32 并与清单中记录的校验和比对，损坏即报错。

## 运行

```bash
npm test     # Node.js 内置测试运行器（node:test）
npm run demo # 崩溃-恢复演示
```

要求 Node.js >= 23.6（原生 TypeScript type-stripping），无第三方依赖。

## 文件结构

- `src/version_set.ts` — VersionSet：版本状态、`logAndApply`、恢复、孤儿清理
- `src/manifest.ts` — MANIFEST 帧格式读写、CRC 校验、撕裂尾部截断
- `src/version_edit.ts` — VersionEdit / FileMetaData 编码解码
- `src/sstable.ts` — SSTable 写入（含 CRC 尾部）与校验
- `src/compaction.ts` — L0→L1 Compaction 的准备/提交两阶段
- `src/crc32.ts` / `src/env.ts` — CRC32 与 fsync/原子写工具
- `test/version_set.test.ts` — 7 个验收测试
- `demo/run.ts` — 崩溃瞬间模拟与恢复演示
