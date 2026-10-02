# LSM 增量备份与硬链接快照导出器

基于 LSM 存储引擎 SSTable **不可变（immutable）**特性的物理备份工具：

- **全量快照**：对当前全部 SSTable 建立硬链接（Hard Link），毫秒级完成、零数据 IO（不复制任何字节，仅 inode 引用计数 +1）。
- **增量导出**：记录基线快照的 SSTable 清单，与当前活跃集合 diff，仅硬链接新增 SSTable 并生成 `DELTA.json` 差异清单（added/removed），支持差异化离线归档。
- **完整性校验**：可选在清单中记录 sha256，`verify()` 校验文件存在性、大小与内容哈希，可检测篡改。

Compaction 删除旧文件（`unlink`）后，快照目录因持有硬链接而完全不受影响——数据块在最后一个链接消失前不会被回收。

## 环境

- Node.js >= 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，零第三方依赖）
- 测试使用 Node 内置测试运行器 `node:test`

```bash
nvm use          # 或任意 >= 22.6 的 Node
npm test         # 运行全部内置测试
npm run demo     # 运行验收场景样例
```

## 结构

```
src/lsm/sstable.ts     SSTable 编码/解码（NDJSON + footer 校验和），写后不可变
src/lsm/engine.ts      LSM 引擎：memtable -> flush -> 不可变 SSTable；compact 合并并 unlink 旧文件
src/backup/snapshot.ts SnapshotManager：全量硬链接快照 / 增量差异导出 / 完整性校验
test/                  node:test 测试（快照 + 增量）
examples/demo.ts       端到端验收场景
```

## API 速览

```ts
import { LSMEngine, SnapshotManager } from './src/index.ts';

const engine = new LSMEngine('./data', { flushThreshold: 1000 });
const backups = new SnapshotManager(engine, './backups');

const snap = backups.createSnapshot({ checksum: true }); // 全量硬链接快照
// ...前台继续写入、compact() 删除旧文件，快照目录不受影响...
backups.verify(snap.id);                                 // { ok: true, ... }
const incr = backups.exportIncremental(snap.id);         // 增量差异导出
```

## 运行样例（`npm run demo`）

```
=== 1. Foreground writes: 5,000 keys (auto-flush to immutable SSTables) ===
   live SSTables: 10 (sst-000001.sst, ..., sst-000010.sst)
   live data: 154940 bytes

=== 2. Zero-copy snapshot (hard links only) ===
   snapshot id: snap-2026-10-02T14-07-26-920Z-2-baseline
   files linked: 10
   hard-link snapshot: 25.00ms (zero data IO)
   + optional sha256 manifest for later integrity checks: 63.25ms
   inode(sst-000001.sst) snapshot=553443994 live=553443994 -> same inode, 0 bytes copied

=== 3. Foreground keeps writing 5,000 more keys, then compaction ===
   compaction merged into sst-000021.sst
   unlinked old files: sst-000001.sst, ..., sst-000020.sst
   live dir now: sst-000021.sst
   original sst-000001.sst still in live dir? false

=== 4. Snapshot is unaffected: verify integrity ===
   verify(snap-...-baseline): ok=true, checked=10 files, errors=0
   keys readable from snapshot: 5000 (expected 5000)
   sample: user-000000=payload-0, user-004999=payload-4999

=== 5. Incremental export since baseline (differential archive) ===
   incremental id: incr-...-from-snap-...-baseline
   added:   [sst-000021.sst]
   removed: [sst-000001.sst, ..., sst-000010.sst]
   verify(incr-...): ok=true

=== 6. Backup inventory ===
   incremental incr-...-from-snap-...-baseline  files=1   seq=21
   full        snap-...-baseline                files=10  seq=10
```

## 测试结果（`npm test`，node:test，8/8 通过）

```
ok 1 - incremental export contains only the SSTables added since the baseline
ok 2 - baseline snapshot + incremental export reconstruct the full dataset
ok 3 - incremental after compaction links merged tables and records removals
ok 4 - diffSnapshots reports added/removed tables between two snapshots
ok 5 - snapshot contains hard links to every live SSTable (same inode, zero copy)
ok 6 - snapshot survives heavy writes + compaction deleting original files
ok 7 - snapshot creation is millisecond-fast and copies zero data bytes
ok 8 - verify detects tampering with a snapshot file
# tests 8
# pass 8
# fail 0
```

## 验收边界对应

| 验收项 | 实现与验证 |
| --- | --- |
| 生成系统快照 | `SnapshotManager.createSnapshot()`，写 `MANIFEST.json`（测试 5） |
| 导出目录包含当时全部 SSTable 硬链接 | 逐文件断言快照与源文件 inode 相同、`nlink >= 2`（测试 5） |
| 密集写入 + Compaction 删除旧文件 | `engine.compact()` 合并并 `unlink` 全部旧 SSTable（测试 6、demo 步骤 3） |
| 快照目录不受影响 | 原文件已不存在，快照 `verify()` 通过且解码出完整数据集（测试 6、demo 步骤 4） |
| 数据完整性 | sha256 清单校验 + 篡改检测（测试 6、8） |
| 零拷贝备份 | 快照与源文件同一 inode，创建耗时毫秒级、与数据量无关（测试 7） |
| 增量 SSTable 清单 / 差异化归档 | `exportIncremental()` 生成 `DELTA.json`（added/removed），基线+增量可还原全量（测试 1-3） |

## 说明与限制

- 快照目录与数据目录须位于**同一文件系统**（硬链接限制）；离线归档阶段可对快照目录做 `tar`/`rsync`。
- `checksum: true` 会在快照时通读一遍数据计算 sha256（可选）；纯硬链接快照本身不触碰任何数据字节。
- 本实现为教学级最小 LSM（单层级、全量合并），备份机制（checkpoint + 硬链接 + 清单 diff）与 RocksDB `Checkpoint`、LevelDB 快照导出的原理一致。
