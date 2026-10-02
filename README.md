
# LSM 增量备份与硬链接快照导出器

纯 Go 标准库实现。包含一个最小 LSM 存储引擎（MemTable + 不可变 SSTable +
Compaction）和一个零拷贝物理备份工具：利用 SSTable 不可变特性，通过硬链接
（hard link）在毫秒级生成一致性快照，并记录增量 SSTable 清单以支持差异化
离线归档。

## 结构

- `lsm/` — LSM 引擎：MemTable 写满后 Flush 为不可变 SSTable；Compaction 合并
  全部 SSTable 并删除旧文件（先落盘新文件，再删旧文件）。
- `backup/` — 快照导出器：
  - `Export()` 把当前全部 SSTable 硬链接到 `snapshot-NNNN/` 目录，零数据拷贝；
  - 每次导出写 `MANIFEST.json`（全量清单：文件名、大小、CRC32）和
    `INCREMENTAL.json`（相对上次导出新增的 SSTable 清单）；
  - `Verify()` 按清单校验快照目录中每个文件的大小与 CRC32。
- `cmd/demo/` — 端到端验收演示。

## 原理

SSTable 落盘即不可变，因此快照只需 `os.Link` 建立硬链接：同一 inode 两个目录
项，无 IO 拷贝。之后 Compaction `unlink` 原文件只是减少链接计数，快照目录仍
持有数据块，数据完整保留。

## 运行

```sh
export GO111MODULE=on GOCACHE=/tmp/gocache   # 本机沙箱需要可写的 GOCACHE
go test ./...
go run ./cmd/demo
```

## 测试结果

```
=== RUN   TestSnapshotSurvivesCompaction
--- PASS: TestSnapshotSurvivesCompaction (0.72s)
=== RUN   TestIncrementalManifest
--- PASS: TestIncrementalManifest (0.02s)
=== RUN   TestVerifyDetectsCorruption
--- PASS: TestVerifyDetectsCorruption (0.00s)
PASS
ok  	example.com/lsmbackup/backup	2.354s
=== RUN   TestPutGetFlushCompact
--- PASS: TestPutGetFlushCompact (0.22s)
=== RUN   TestReopenRecoversSSTables
--- PASS: TestReopenRecoversSSTables (0.01s)
=== RUN   TestSSTableRoundTrip
--- PASS: TestSSTableRoundTrip (0.17s)
PASS
ok  	example.com/lsmbackup/lsm	1.759s
```

关键验收测试 `TestSnapshotSurvivesCompaction`：快照后用 `os.SameFile` 断言
快照文件与原文件是同一 inode（零拷贝）；随后 6 轮密集写入 + Compaction 删除
全部原始文件；最后 `Verify` 校验快照 CRC 通过，且能从快照 SSTable 读回原始
数据。

## 运行样例（go run ./cmd/demo）

```
== phase 1: load 3 generations x 2000 keys ==
live sstables: [sst-000001.sst sst-000002.sst sst-000003.sst]

== phase 2: export snapshot #1 (hard links, zero copy) ==
exported 3 sstables to snapshot-0001 in 3.561833ms
  sst-000001.sst       244008 bytes  crc32=9a12d628
  sst-000002.sst       244008 bytes  crc32=80cd1447
  sst-000003.sst       244008 bytes  crc32=afad52f6

== phase 3: heavy foreground writes (5 more generations) ==
live sstables: [sst-000001.sst sst-000002.sst sst-000003.sst sst-000004.sst sst-000005.sst sst-000006.sst sst-000007.sst sst-000008.sst]

== phase 4: export snapshot #2 (incremental) ==
snapshot #2 full manifest: 8 sstables
snapshot #2 incremental list (new since #1): [sst-000004.sst sst-000005.sst sst-000006.sst sst-000007.sst sst-000008.sst]

== phase 5: compaction deletes the original files ==
live sstables after compaction: [sst-000009.sst]
  original sst-000001.sst     deleted from live dir: true
  original sst-000002.sst     deleted from live dir: true
  original sst-000003.sst     deleted from live dir: true

== phase 6: verify both snapshots ==
snapshot #1 CRC verification: OK (data intact after compaction)
snapshot #2 CRC verification: OK

ALL CHECKS PASSED
```
