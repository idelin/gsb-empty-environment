# LSM 增量备份与硬链接快照导出器

面向 LSM 存储引擎的全量 / 增量物理备份工具。利用 SSTable 的**不可变（immutable）**特性，
通过 `fs.linkSync` 建立硬链接实现**毫秒级、零 IO 消耗**的快照；增量模式记录新增 SSTable 清单，
支持差异化离线归档。

## 组件

| 文件 | 说明 |
| --- | --- |
| `src/lsm/engine.ts` | 最小可用的 LSM 引擎：memtable → flush 为不可变 SSTable，Compaction 合并后删除旧文件 |
| `src/backup/exporter.ts` | 快照导出器：`exportFull` 全量硬链接、`exportIncremental` 增量硬链接、`verify` 校验 |
| `test/snapshot.test.ts` | Node.js 内置 `node:test` 验收测试（4 个用例） |
| `examples/demo.ts` | 完整验收场景运行样例 |

## 原理

- SSTable flush 后字节永不改变，生命周期只有「flush 创建」与「Compaction unlink」两个事件。
- 快照 = 为每个存活 SSTable 建立一个额外目录项（硬链接），指向同一 inode：
  不复制任何数据块，耗时微秒级/文件。
- Compaction 删除引擎侧文件名后，快照侧的链接让 inode 存活，数据零丢失；
  此时快照成为数据唯一持有者（`nlink` 从 2 降为 1）。
- 每个快照目录带 `MANIFEST.json`，记录文件清单、大小与 SHA-256，可随时离线校验。
- 增量快照以上一次 manifest 为基线，只链接新出现的 SSTable，已归档文件直接跳过。

## 运行

要求 Node.js ≥ 22.6（使用内置 type stripping 直接运行 TypeScript，无任何第三方依赖）。

```bash
npm test        # node:test 验收测试
npm run demo    # 完整验收场景样例
```

## 测试结果

```
ok 1 - full snapshot hard-links every live SSTable (zero-copy: same inode)
ok 2 - snapshot is unaffected by subsequent heavy writes and compaction deleting old files
ok 3 - incremental snapshot exports only new SSTables; full + delta reconstruct the live set
ok 4 - verify detects tampering with snapshot content
# tests 4
# pass 4
# fail 0
```

## 运行样例输出（`npm run demo`）

```
=== LSM hard-link snapshot exporter demo ===

[1] loading 20,000 keys (flush threshold 2,000) ...
    live SSTables: 3, data size: 1,679,960 bytes

[2] taking FULL snapshot ...
    linked 3 SSTables in 6.99 ms (zero-copy)
    inode check: data/000009.sst ino=549768710 == backup/000009.sst ino=549768710 (nlink=2)

[3] writing 3,000 more keys, then INCREMENTAL snapshot ...
    delta: 2 new SSTables linked in 1.23 ms, 3 already archived (skipped)

[4] foreground churn: 30,000 more writes + forced compaction ...
    live SSTables after compaction: 1 (000032.sst)
    baseline files deleted from data dir by compaction: 3/3

[5] verifying snapshot integrity ...
    001-full: OK (1679960 bytes intact, 3 file(s) now solely owned by snapshot)
    002-inc: OK (258000 bytes intact, 2 file(s) now solely owned by snapshot)

Demo complete. Snapshots preserved every generation despite compaction.
```

验收边界对应关系：

- **生成系统快照** → 步骤 [2]，3 个 SSTable 全量硬链接，耗时 < 10ms；
- **导出目录包含当时全部 SSTable 硬链接** → inode 相同、`nlink=2`，零拷贝；
- **前台密集写入 + Compaction 删除旧文件** → 步骤 [4]，基线文件 3/3 被删除；
- **快照目录不受影响、数据完整** → 步骤 [5]，SHA-256 校验通过，快照成为数据唯一持有者。
