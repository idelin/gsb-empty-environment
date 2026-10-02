# Crash-Consistent VersionSet Manager

LSM 树元数据持久化与原子版本演进器（TypeScript / Node.js，零依赖）。
记录每一层当前的 SSTable 文件列表与校验和；Compaction 通过追加
VersionEdit 日志原子切换 VersionSet，崩溃重启后准确恢复最新目录结构。

## 设计

```
MANIFEST-000001   追加式日志，每条记录 = [len u32][crc32 u32][VersionEdit JSON]
CURRENT           指向当前 MANIFEST 文件
000123.sst        SSTable：有序 KV 条目 + 尾部 CRC-32 校验和
```

原子提交流程（`compactL0ToL1`，见 `src/compaction.ts`）：

1. 合并 L0 输入，写出新 L1 SSTable 并 fsync —— 此刻崩溃，新文件是孤儿；
2. `logAndApply(edit)`：把 VersionEdit（删 L0 输入 + 加 L1 输出）作为
   **一条** 记录追加到 MANIFEST 并 fsync —— 版本切换的唯一原子点；
3. 提交成功后才删除被替换的 L0 文件。

崩溃恢复（`VersionSet.open`，见 `src/versionSet.ts`）：

- 读取 CURRENT → 重放 MANIFEST 中所有校验通过的记录，重建各层文件列表；
- 长度/CRC 校验失败的尾部记录视为撕裂写（torn write），截断丢弃；
- `removeObsoleteFiles()` 删除磁盘上不被当前版本引用的孤儿 SSTable。

## 运行

需要 Node.js >= 22.6（使用内置 `--experimental-strip-types` 直接运行 TS，
无需安装任何依赖）。

```bash
npm test     # node:test 内置测试
npm run demo # 崩溃-恢复全流程演示
```

## 验收场景

正在执行 L0→L1 Compaction 写出新文件的瞬间模拟崩溃
（`crashBeforeCommit: true`）；重启后系统读取 MANIFEST 日志，丢弃未完成
提交的孤儿 SSTable，完整恢复到 Compaction 发生前的有效版本。
对应测试：`test/versionSet.test.ts` 中
"crash during L0->L1 compaction output write"。

## 文件结构

- `src/crc32.ts` — CRC-32 实现
- `src/versionEdit.ts` — VersionEdit 编解码（增删文件、nextFileNumber）
- `src/manifest.ts` — MANIFEST 追加写入 / 重放 / 撕裂尾部截断
- `src/sstable.ts` — SSTable 构建、读取、校验和验证
- `src/versionSet.ts` — VersionSet：logAndApply 原子提交、恢复、孤儿清理
- `src/compaction.ts` — L0→L1 Compaction（含崩溃注入点）
- `test/versionSet.test.ts` — 6 个 node:test 用例
- `examples/demo.ts` — 端到端演示
