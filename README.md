# LSM 增量备份与硬链接快照导出器

基于 **SSTable 不可变性** 的 LSM 存储引擎物理备份工具，纯 Go 标准库实现：

- **零拷贝快照**：对不可变 SSTable 建立硬链接（Hard Link），毫秒级导出、零数据 IO；
- **全量清单**：每次快照写入 `MANIFEST.json`（文件名 + size + CRC32）；
- **增量清单**：每次快照写入 `DELTA.json`，仅记录相对上一快照新增的 SSTable，支持差异化离线归档；
- **完整性校验**：`backup.Verify` 重算 CRC32 比对清单，确认快照在源文件被 Compaction 删除后依然完好。

## 结构

```
lsm/            极简 LSM 引擎：memtable + WAL + 不可变 SSTable + 合并式 Compaction
  sstable.go    SSTable 格式：magic | count | entries... | crc32，写后不可变
  db.go         DB：Put/Get/flush/Compact，ViewSSTables 提供一致性视图
backup/         备份组件
  backup.go     Snapshotter：硬链接快照 + 全量/增量清单 + Verify 校验
cmd/demo/       可运行样例：完整走一遍验收流程
```

## 一致性原理

`Snapshotter.Snapshot` 通过 `DB.ViewSSTables` 在 **读锁** 内枚举当前全部 SSTable 并建立硬链接；
Compaction 删除旧文件需要 **写锁**，因此快照期间文件集不会变化。
SSTable 落盘即不可变，硬链接与源文件共享同一 inode——Compaction 的 `unlink`
只是减少链接数，快照目录仍持有数据，实现零拷贝的时点一致性快照。

## 运行样例

```sh
go run ./cmd/demo
```

输出（实际运行）：

```
== 阶段 1: 密集写入，触发 memtable flush ==
已生成 7 个不可变 SSTable

== 阶段 2: 生成硬链接快照（零拷贝） ==
快照 snap-000001 完成，耗时 25.358083ms，包含 7 个 SSTable 硬链接:
  sst-000001.sst        66857 bytes  crc=5a44c765  与源文件同inode(零拷贝)=true
  ...
快照逻辑大小 467999 bytes，硬链接耗时毫秒级、无数据拷贝

== 阶段 3: 前台继续密集写入 + Compaction 删除旧文件 ==
Compaction 完成：删除旧 SSTable 23 个，当前存活 1 个
快照对应的 7/7 个源文件已被 Compaction 物理删除

== 阶段 4: 校验快照目录数据完整性 ==
MANIFEST 校验通过：全部硬链接文件 size + CRC32 与快照时刻一致
快照中 batch1-key-000000 = batch1-value-000000-...（快照时刻旧值）
DB 中  batch1-key-000000 = batch3-value-000000-...（覆盖后新值）

== 阶段 5: 第二次快照，展示增量清单 ==
快照 snap-000002: 全量清单 5 个文件，增量清单（DELTA.json）仅 5 个新增 SSTable:
  + sst-000024.sst      1022491 bytes
  ...
全部验收通过：快照不受 Compaction 删文件影响，数据完整，零拷贝。
```

## 测试

```sh
go test ./... -v
```

结果（全部通过）：

```
=== RUN   TestSnapshotSurvivesCompaction   # 核心验收：快照后 Compaction 删除全部源文件，快照完好
--- PASS: TestSnapshotSurvivesCompaction (0.92s)
=== RUN   TestIncrementalSnapshots         # 增量清单只含新增 SSTable；三次快照均可校验
--- PASS: TestIncrementalSnapshots (0.33s)
=== RUN   TestSnapshotterResume            # 重启后延续序号与增量链
--- PASS: TestSnapshotterResume (0.16s)
=== RUN   TestVerifyDetectsCorruption      # 篡改快照字节可被 CRC 校验发现
--- PASS: TestVerifyDetectsCorruption (0.08s)
ok      snapshotlsm/backup

=== RUN   TestFlushGetAndCompaction        # flush/compaction 后读写正确、覆盖写新值胜出
--- PASS: TestFlushGetAndCompaction (0.59s)
=== RUN   TestManualCompactDeletesObsoleteFiles
--- PASS: TestManualCompactDeletesObsoleteFiles (0.06s)
=== RUN   TestWALReplay                    # 崩溃恢复：WAL 回放
--- PASS: TestWALReplay (0.00s)
=== RUN   TestSSTableRoundTrip             # SSTable 编解码 + CRC 检错
--- PASS: TestSSTableRoundTrip (0.00s)
ok      snapshotlsm/lsm
```

### 核心验收测试 `TestSnapshotSurvivesCompaction` 的断言链

1. 快照目录包含当时全部 SSTable，且每个文件与源文件 **同 inode**（零拷贝硬链接）；
2. 继续密集写入（含覆盖写）并强制 Compaction，**快照对应的源文件全部被物理删除**；
3. `Verify(快照目录)` 通过：size + CRC32 与清单一致；
4. 快照内 SSTable 逐条记录与快照时刻内容 **逐字节相等**；
5. 被覆盖的 key：快照中仍是旧值，DB 中是新值——时点隔离成立。
