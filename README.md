# bitmap — 压缩位图：增量快照与检查点归档（Go）

Roaring 风格的压缩位图实现，仅依赖标准库。支持三种容器自动转换、
批量 AND/OR/XOR 集合运算，以及基于 Copy-on-Write 的轻量级增量快照
与检查点（Checkpoint）归档恢复。

## 设计

**容器（低 16 位存储，高 16 位索引）**

| 容器 | 结构 | 自动转换规则 |
|---|---|---|
| `ArrayContainer` | 有序 `[]uint16` | 基数 > 4096 升级为 `BitmapContainer` |
| `BitmapContainer` | `[1024]uint64` 位向量 | 基数降回 ≤ 4096 降级为 `ArrayContainer`；运算结果若 RLE 更紧凑（4×runs < 8KB）转为 `RunContainer` |
| `RunContainer` (RLE) | 有序闭区间列表 | 区间碎片化（4×runs 超过数组/位图开销）时自动降级 |

`Add` 走数组→位图路径；`AddRange` 直接生成 RLE 容器；所有集合运算
的结果经 `finishBitmap`/`maybeConvert` 重新选择最优容器。

**批量运算**：`And`/`Or`/`Xor` 接受任意多个位图，按键归并容器索引，
容器级运算按 (array, bitmap, run) 类型对分发到 6 种组合；输入位图按
指针序加读锁（去重），无死锁。运算为纯函数，不修改输入。

**增量快照与检查点**

- `Snapshot()`：O(容器数) 复制索引切片，容器共享；此后写线程按
  「容器代际 ≤ 最大活跃快照代际」触发 COW，快照永远不可变。
  `Release()` 后位图恢复就地写入，零 COW 开销。
- `Checkpointer.Checkpoint()`：写全量 `chk-<gen>.bin`。
- `Checkpointer.Incremental()`：只写代际晚于上次归档的变更容器与
  删除墓碑（`inc-<base>-<gen>.bin`），开销与变更量成正比。
- 归档期间通过 `pinView`/`pinDelta` 在同一临界区内注册内部快照并
  拷贝索引，序列化不持锁、写线程仅对涉及容器付一次 COW 克隆，
  停顿仅为索引拷贝（微秒级）。
- 文件先写 `.tmp` 再原子 rename；头部含 magic/version/代际链校验，
  损坏或代际不匹配的归档会在 `Restore` 时报错。
- `Restore(dir)`：加载最新检查点，沿代际链依次应用增量。

## 运行样例

```bash
go run ./cmd/demo
```

```
== 容器自动选择 ==
元素总数=85100 容器: 数组=2 位图=1 RLE=2

== 批量集合运算 ==
AND: 132.625µs  结果基数=33521664
OR : 91.417µs   结果基数=33587200
XOR: 130.916µs  结果基数=65536

== 增量快照与检查点归档 ==
创建快照: 11µs（仅复制索引，COW 隔离）
全量检查点: chk-00000000000000005104.bin (8481 字节)
增量快照  : inc-00000000000000005104-00000000000000005171.bin (317 字节，仅含变更容器)
快照基数=85100（隔离） 当前基数=1085050
恢复校验  : 与当前位图差异元素=0（应为 0）
```

两个各含 3355 万元素（512 个满容器）的位图，交/并/异或均在
**百微秒级**完成，即每容器对（65536 元素）约 0.2–7µs。

## 测试

```bash
go test ./...        # 31 个测试
go test -race ./...  # 并发安全（-race 全绿）
go test -run xxx -bench . -benchtime 100ms .
```

测试结果（Go 1.11, darwin/amd64）：

```
--- PASS: TestSparseIDsAutoArray              # 稀疏 ID -> ArrayContainer
--- PASS: TestDenseIDsAutoBitmap              # 密集 ID -> BitmapContainer
--- PASS: TestRangeAutoRun                    # 连续区间 -> RunContainer
--- PASS: TestArrayToBitmapTransition         # 4096 阈值升级
--- PASS: TestBitmapToArrayTransition         # 降回阈值降级
--- PASS: TestRunDegradesWhenFragmented       # RLE 碎片化自动降级
--- PASS: TestContainerEmptiedIsRemoved       # 空容器回收 + 墓碑
--- PASS: TestOpsAgainstReference             # 20 轮随机对照参考实现
--- PASS: TestBatchOps                        # 5 位图批量 AND/OR/XOR
--- PASS: TestOpsWithEmpty                    # 空位图边界
--- PASS: TestOpsDoNotMutateInputs            # 运算纯度
--- PASS: TestFullDomainRange                 # 全 2^32 域
--- PASS: TestIteratorSorted                  # 迭代器严格升序
--- PASS: TestConcurrentReadWrite             # 读写/快照并发 (-race)
--- PASS: TestConcurrentCheckpointer          # 归档与写入并发 (-race)
--- PASS: TestSnapshotIsolation               # 快照隔离（COW）
--- PASS: TestSnapshotReleaseRestoresInPlaceWrites
--- PASS: TestSnapshotIteratorConsistent
--- PASS: TestSaveLoadRoundTrip               # 序列化往返
--- PASS: TestReadBitmapRejectsGarbage        # 损坏输入拒绝
--- PASS: TestCheckpointRestoreRoundTrip      # 检查点恢复 + 状态不共享
--- PASS: TestIncrementalChain                # 增量链（含墓碑/边界值）
--- PASS: TestIncrementalNoChanges            # 空增量
--- PASS: TestIncrementalOnlyWritesChangedContainers  # 增量体积 << 全量
--- PASS: TestRestorePicksLatestCheckpoint
--- PASS: TestRestoreMissingCheckpoint
--- PASS: TestRestoreCorruptFile
--- PASS: TestIncrementalBaseMismatch         # 代际链校验
--- PASS: TestCheckpointAtomicNoTempLeft      # 原子写无残留
--- PASS: TestNoLeaks                         # goroutine/簿记无泄漏
--- PASS: TestCOWDisabledAfterRelease         # 释放后无 COW 残留
ok  bitmap
```

基准（`-benchtime 100ms`，512 容器对 ≈ 3355 万元素/位图）：

```
BenchmarkAndDense-10          ~2-6 ms/op   (稠密位图 AND，~4-12µs/容器对)
BenchmarkOrDense-10           ~2-6 ms/op
BenchmarkXorDense-10          ~1-4 ms/op
BenchmarkAndRunContainers-10  ~65-240 µs/op (RLE AND，~0.1-0.5µs/容器对)
BenchmarkAndSparse-10         ~1.5-3.5 ms/op (10 万稀疏点)
BenchmarkSnapshot-10          ~4-15 µs/op  (1024 容器快照创建)
BenchmarkAddDense-10          ~100-500 ns/op (单点插入)
```

按容器对换算，百亿（10^10）量级元素的交并运算可分解为约 15.3 万个
独立容器对运算，每对微秒级（RLE 容器对亚微秒），整体毫秒级完成；
快照创建与增量归档开销与容器数/变更量成正比，均为微秒级停顿。

## API 一览

```go
bm := bitmap.New()
bm.Add(42); bm.AddBatch(1, 2, 3); bm.AddRange(100, 200000); bm.Remove(42)
bm.Contains(42); bm.Cardinality(); bm.Stats(); bm.Iterator()

snap := bm.Snapshot()          // 不可变视图
defer snap.Release()

inter := bitmap.And(a, b, c)   // 批量交
union := bitmap.Or(a, b, c)    // 批量并
diff  := bitmap.Xor(a, b, c)   // 批量对称差

cp, _ := bitmap.NewCheckpointer(bm, dir)
cp.Checkpoint()                // 全量
cp.Incremental()               // 增量（仅变更）
restored, _ := bitmap.Restore(dir)
```
