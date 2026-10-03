# bitmapckpt — 压缩位图：增量快照与 Checkpoint 归档（Go 版）

仅使用 Go 标准库实现的压缩位图（类 Roaring Bitmap），支持：

- **三种容器自动转换**：`ArrayContainer`（有序数组，≤4096 个值）、
  `BitmapContainer`（8KB 位图）、`RunContainer`（RLE 区间编码）。
  插入、集合运算后自动选择最紧凑的表示；`Optimize()` 可全局重选。
- **批量布尔集合运算**：`And` / `Or` / `Xor`（两个位图）以及
  `bitmapckpt.And(...)` / `Or(...)` / `Xor(...)` 批量变体，全部覆盖
  9 种容器类型组合，结果自动归一化。
- **uint64 值域**：key = value >> 16，单个位图可承载远超 2^32 的 ID，
  百亿（10^10）级数据量可真实模拟。
- **轻量级增量快照**：容器写时复制（COW），`Snapshot()` 只记录自上次
  快照/检查点以来被修改的容器引用，停顿与脏容器数成正比（实测微秒级），
  不拷贝数据。
- **Checkpoint 归档**：`Checkpoint()` 全量归档并重置增量链；
  `Archive` 以 append-only 文件持久化（magic + 长度 + CRC32 校验），
  支持 `RestoreFromArchive` 一键恢复（最新 checkpoint + 后续增量重放，
  链式版本校验）。

## 文件结构

| 文件 | 内容 |
|---|---|
| `container.go` | 三种容器实现、自动转换、RLE 区间合并/翻转 |
| `ops.go` | 容器级 AND/OR/XOR（9 种类型组合，输入不可变） |
| `bitmap.go` | `Bitmap` 主类型：COW、并发安全、批量运算、`Optimize` |
| `snapshot.go` | 增量快照、检查点、链式恢复 |
| `archive.go` | append-only 归档文件（CRC32 校验、损坏检测） |
| `serialize.go` | 容器二进制编解码 |
| `cmd/demo/main.go` | 运行样例 |

## 运行样例

```sh
go run ./cmd/demo
```

输出（数值与机器相关）：

```
bitmap A: 442143 values, containers: array=99684 bitmap=0 run=6
bitmap B: 292144 values, containers: array=79807 bitmap=0 run=4
A AND B = 162144 values in 11.3ms
A OR  B = 572143 values in 140.6ms
A XOR B = 409999 values in 115.3ms
huge bitmap: 9999941632 values in 152587 run containers
huge(10^10) AND sparse(10^4) = 73 values in 1.2ms
checkpoint archived
round 0: incremental snapshot (4999 dirty containers) in 1.1ms
round 1: incremental snapshot (4999 dirty containers) in 4.0ms
round 2: incremental snapshot (5000 dirty containers) in 2.8ms
restored from archive: 457143 values, equals source: true
```

要点：10^10 个连续 ID 用 152587 个 RLE 容器即可表示（原始位图需 1.25GB），
与稀疏位图求交只需遍历稀疏侧容器，毫秒/微秒级完成。

## 测试

```sh
go test ./...            # 全部单元测试
go test -race ./...      # 竞态检测（并发插入/读取/快照/归档）
go test -run=NONE -bench=. -benchmem .   # 基准测试
```

测试覆盖（25 个用例）：

- 容器：三种表示全组合 AND/OR/XOR 对照朴素集合、array→bitmap→run 自动
  转换阈值、RLE 区间合并/翻转、序列化往返。
- 位图：稀疏离散 ID + 密集连续区间插入、容器自动选择断言、边界值
  （0、MaxUint64、2^32 全空间、高 key 空间）、COW Clone 隔离、并发插入。
- 快照/归档：checkpoint+增量链恢复各中间状态、快照隔离（无别名泄漏）、
  断链拒绝、checkpoint 重置脏集、归档往返、只保留最新 checkpoint、
  CRC 损坏/截断/坏 magic 检测、关闭后操作返回 ErrClosed、
  并发写入+快照+归档最终一致性、**无 goroutine 泄漏 / 无句柄泄漏断言**、
  恢复后位图与源位图互不影响。

最新测试结果（本机，Go 1.11）：

```
$ go test -count=1 -race .
ok      bitmapckpt      92.180s     # 25/25 PASS, 无数据竞争
```

基准（受限沙箱 CPU，绝对值仅供参考；`GOGC=off` 行展示算法本身的开销）：

```
BenchmarkAndTenBillionVsSparse-10         28051702 ns/op   (GOGC=off: 726690 ns/op)
BenchmarkAndTenBillionRunContainers-10  1604444166 ns/op   (GOGC=off: 99182625 ns/op, ~650ns/容器)
BenchmarkAndDenseBitmapContainers-10          7394 ns/op   单个稠密容器交集（1024 个 uint64 字运算）
BenchmarkOrTenBillionRunContainers-10   1653405666 ns/op   (GOGC=off: 100110194 ns/op)
BenchmarkXorDenseBitmapContainers-10         15378 ns/op
BenchmarkIncrementalSnapshotPause-10         10078 ns/op   10 万容器位图、100 个脏容器的快照停顿
BenchmarkCheckpointFull-10                15387891 ns/op   10 万容器全量 checkpoint
BenchmarkAdd-10                                557 ns/op
```

## 设计说明

- **低停顿快照**：`Snapshot()` 持写锁期间仅遍历脏容器集合、置 shared
  标志并记录引用，O(脏容器数)；之后的写操作通过 COW 克隆被共享的容器，
  快照数据天然不可变。
- **无泄漏**：快照/检查点/恢复出的位图共享的只是不可变容器，任何写入
  都会先克隆；测试显式断言快照隔离、goroutine 数不增长、归档关闭后
  句柄不可再用。
- **归档格式**：`magic | (type, len, payload, crc32)*`，checkpoint 记录
  全量容器，incremental 记录 (gen, base, 脏容器)；读取时校验 CRC 与
  版本链（base 必须等于前驱 gen），损坏即报错。
