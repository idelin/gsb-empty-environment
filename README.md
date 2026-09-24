# blkmap — 逻辑偏移到不可变物理块的区间映射

纯标准库实现的稀疏区间映射（extent map）：逻辑偏移区间映射到**不可变物理块**，
支持写入（copy-on-write）、打洞（punch hole）和快照。所有状态放在调用方指定的
目录中，故障通过注入点模拟。

## 目录布局

```
<dir>/
  manifest.json        # 区间映射 + 快照 + 下一个块 id（原子替换）
  blocks/<id>.blk      # 不可变物理块，写一次，永不修改
```

`Extent{Off, Len, Block, BOff}` 表示逻辑区间 `[Off, Off+Len)` 映射到物理块
`Block` 内偏移 `BOff` 处。区间始终有序、不重叠，相邻且引用同一块连续区域的
区间会被合并；未被任何区间覆盖的范围是**洞**，读为零且不占用物理块。

## 核心语义

- **写入 `WriteAt`**：按块对齐切分，每个受影响的物理块都新分配（COW）。
  不满整块时先把旧数据（洞按零）读入新块再覆盖，因此**跨块写入只改覆盖区**，
  两侧旧数据原样保留。
- **打洞 `PunchHole`**：删除与目标范围重叠的区间，边界处在块内**分裂**，
  中段被打穿后**两侧保留**；洞读为零，不分配任何物理块。
- **快照 `Snapshot`**：把当前区间表复制进 manifest。物理块不可变，所以
  后续写入/打洞**绝不影响旧快照**；快照本身不分配新块。
- **故障注入**：`Store.Inject func(point string) error` 在注入点返回错误即
  模拟崩溃，磁盘状态与真实崩溃/断电完全一致，重开目录即走恢复路径。
  注入点：`block.staged`、`block.committed`、`manifest.staged`、
  `manifest.committed`。

## 崩溃恢复

`Open` 时执行恢复：

1. 读取 `manifest.json`（若存在）——它永远是某个完整提交的版本；
2. 删除 `blocks/*.tmp` 和 `manifest.json.tmp` 等残留临时文件；
3. 回收不被 manifest 或任何快照引用的**孤儿物理块**（崩溃发生在块已提交、
   manifest 未提交之间时产生）。

因此崩溃后的可见状态永远是**上一次成功提交的写/打洞/快照**，未完成的操作
要么完全不可见，要么（manifest 已提交后崩溃）完全生效——不存在中间态。

## 断电保证边界

**保证**（依赖文件系统遵守 fsync/rename 语义，如 ext4/xfs/APFS 开启屏障）：

- 物理块经 `写临时文件 → fsync → rename` 发布：崩溃后要么不存在、要么完整，
  绝不出现半个块；已发布的块永不被改写。
- manifest 经 `写临时文件 → fsync → rename → fsync(目录)` 原子切换：
  断电后读到旧版本或新版本，绝不读到撕裂内容。
- `WriteAt`/`PunchHole`/`Snapshot` 返回 nil 即表示本次变更已持久化；
  断电最多丢失最近一次未返回成功的操作，绝不损坏更早的已提交状态和快照。

**不保证**（明确边界）：

- 单写者模型（进程内互斥锁）；不处理多进程并发写同一目录。
- 不防御设备层撕裂写之外的故障：无校验和，不检测位腐烂/静默数据损坏
  （可作为后续工作：块级 CRC + 读时校验）。
- 不保证底层文件系统忽略 fsync 的硬件（如禁用写屏障的磁盘）上的持久性。
- 注入错误返回后，内存中的 Store 可能与磁盘不一致，应重新 `Open`
  以获得持久化状态（测试即如此验证）。

## 运行样例

```
$ go run ./cmd/demo /tmp/blkmap-demo
store dir: /tmp/blkmap-demo (block size 16)

write 40 bytes:              "AAAAAAAAAAAAAAAABBBBBBBBBBBBBBBBCCCCCCCC"
cross-block write @12:       "AAAAAAAAAAAAxxxxxxxxBBBBBBBBBBBBCCCCCCCC"
after punch [20,28):         "AAAAAAAAAAAAxxxxxxxx\x00\x00\x00\x00\x00\x00\x00\x00BBBBCCCCCCCC"
snapshot before-punch:       "AAAAAAAAAAAAxxxxxxxxBBBBBBBBBBBBCCCCCCCC"
after reopen:                "AAAAAAAAAAAAxxxxxxxx\x00\x00\x00\x00\x00\x00\x00\x00BBBBCCCCCCCC"

extents after reopen: [{Off:0 Len:16 Block:4 BOff:0} {Off:16 Len:4 Block:5 BOff:0} {Off:28 Len:4 Block:5 BOff:12} {Off:32 Len:16 Block:3 BOff:0}]
physical blocks on disk: 3 (holes occupy none)
```

可以看到：跨块写入只改了 `[12,20)`；打洞 `[20,28)` 后块 5 被分裂为
`[16,20)` 和 `[28,32)` 两段，两侧数据保留；快照仍是打洞前的完整数据。

## 测试结果

```
$ go test -v ./...
--- PASS: TestWriteReadRoundTrip (0.09s)
--- PASS: TestCrossBlockWriteOnlyTouchesCoveredRange (0.10s)
--- PASS: TestPunchHoleMiddleKeepsBothSides (0.10s)
--- PASS: TestHoleOccupiesNoPhysicalBlock (0.12s)
--- PASS: TestSnapshotImmuneToLaterPunchAndWrite (0.25s)
--- PASS: TestPersistenceAcrossReopen (0.15s)
--- PASS: TestCrashBeforeManifestCommitKeepsOldState (0.10s)
--- PASS: TestCrashDuringBlockWriteLeavesNoTrace (0.14s)
--- PASS: TestCrashAfterManifestCommitIsDurable (0.04s)
--- PASS: TestRandomOpsAgainstReferenceModel (9.80s)
--- PASS: TestSnapshotAfterReopenAndDelete (0.12s)
PASS
ok  	blkmap	11.091s
```

其中 `TestRandomOpsAgainstReferenceModel` 用 300 次随机写/打洞（含周期性
重开）对照内存参考模型验证数据一致性与区间不重叠不变式。
