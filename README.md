# paircache — 2Q（分段 LRU）缓存，含普通 LRU 对照

仅用 Go 标准库实现的固定容量内存缓存，首次访问与重复访问分离，
并附带一个共享同一套租约/记账/注入接口的普通 LRU 基线，统计口径完全一致，
可直接对照。**本库不宣称 2Q 在所有流量下都优于 LRU**，见下文反例。

## 策略结构（2Q）

- **A1in（recent）**：首次访问进入的有界 FIFO，占容量 `RecentRatio`（默认 25%）。
  顺序扫描只流经 A1in，不会挤掉热队列。
- **A1out（ghost）**：从 A1in 淘汰（或被 Delete）的键留下仅含元数据的幽灵记录，
  按 FIFO 回收，数量受 `GhostLimit` 约束，删除再多也不会无限增长。
- **Am（freq）**：再次访问（命中幽灵记录）才晋升进入的热 LRU，占其余容量。

## 关键语义

- **变长条目**：容量按字节计算（`CapacityBytes`），每条目按实际载荷大小记账；
  单条大于总容量的条目直接不缓存。
- **读租约**：`Acquire` 返回 `Lease`，淘汰只是逻辑移除；仍被借用的条目内存
  在最后一个 `Release` 之前不释放。因此物理占用可能暂时超过逻辑容量，
  `Stats.PeakPhysicalBytes` 记录该峰值，`OnFree` 回调在真正释放时触发。
- **可注入依赖**：数据源 `Loader`、时钟 `Now`、释放回调 `OnFree` 全部可注入；
  不依赖任何远程缓存，失败通过 Loader 返回 error 注入。
- **元数据可恢复**：`Snapshot`/`RestoreTwoQ`/`RestoreLRU` 只序列化策略元数据
  （队列成员、顺序、大小、时间戳），不序列化值；恢复后的条目首次访问时经
  Loader 惰性重载，策略判定（命中/淘汰序列）与原始访问回放完全一致
  （见 `TestRestoreReplayConsistency2Q/LRU`）。

## 运行

```sh
go test ./...          # 行为测试（含 -race 可通过）
go run ./cmd/tracebench # 2Q vs LRU 多轨迹对照
```

## 对照指标（真实输出，`go run ./cmd/tracebench`）

容量 64 KiB，条目 64..255 B 变长：

```
trace     pol      ops    hits    hit%  ghost   evict    peakB    ns/op
scan      2q      3600       0    0.0%      0    3498    16620      684
scan      lru     3600       0    0.0%      0    3189    65784      687
hot+scan  2q      3500     300    8.6%    100    2998    32618      686
hot+scan  lru     3500       0    0.0%      0    3090    65784      666
loop2x    2q      4250     889   20.9%   1629    2950    65787      625
loop2x    lru     4250       0    0.0%      0    3840    65782      511
zipf      2q     20000   16654   83.3%   1515    2931    65787      230
zipf      lru    20000   16880   84.4%      0    2704    65786      205
uniform   2q     20000    1911    9.6%   4944   17674    65788      931
uniform   lru     20000    2043   10.2%      0   17546    65791      687

uniform 2q with 16-lease window: peak physical = 66144 bytes (logical capacity 65536)
```

解读：

- **hot+scan**：2Q 的热集在 Am 中不被扫描冲掉（300 次热键命中），LRU 为 0。
  这是 2Q 的设计目标场景。
- **scan**：两者都 0 命中（纯扫描无复用），但 2Q 的峰值物理占用仅 16 KiB
  （扫描键永远进不了 Am），LRU 则填满全部 64 KiB。
- **反例 1（zipf）**：LRU 84.4% > 2Q 83.3%。2Q 把 25% 容量让给 A1in，
  热集可驻留空间更小，偏斜复用流量下略亏。
- **反例 2（uniform）**：LRU 10.2% > 2Q 9.6%，且 2Q 每次操作更慢、
  淘汰更多。无局部性的流量下幽灵簿记纯属开销。
- **租约演示**：持有 16 个租约时峰值物理占用 66144 B > 逻辑容量 65536 B，
  证明被淘汰但仍在借用的内存不会提前释放。

## 测试结果（`go test -v ./...`，19 个用例全部通过）

```
--- PASS: TestLRUBasic
--- PASS: TestLRULeasePinsEvictedMemory
--- PASS: TestLRUVariableSizeEntries
--- PASS: TestLRUDelete
--- PASS: TestRestoreReplayConsistency2Q
--- PASS: TestRestoreReplayConsistencyLRU
--- PASS: TestSnapshotDeterministic
--- PASS: TestRestoreReloadsValuesLazily
--- PASS: TestRestoreRejectsWrongKind
--- PASS: TestSameKeyRepeatHitPromotion
--- PASS: TestScanResistanceVsLRU
--- PASS: TestCapacityExactlyOneEntry
--- PASS: TestCapacityOneByte
--- PASS: TestLeasePinsEvictedMemory
--- PASS: TestDeleteGhostRecordsBounded
--- PASS: TestLoaderFailureInjection
--- PASS: TestClockInjection
--- PASS: TestResourceRelease
--- PASS: TestConcurrentAcquireRelease   (go test -race 亦通过)
PASS
ok  	paircache
```

覆盖：同键重复命中经幽灵记录正确晋升 Am；容量恰为 1 条 / 1 字节的边界；
删除后幽灵记录按 FIFO 有界回收；租约钉住已淘汰内存、释放后物理占用归零
（每条目 OnFree 恰好一次）；Loader 失败注入不污染缓存；注入时钟的时间戳
进入快照且可恢复；恢复快照后回放与原运行的命中/淘汰序列一致。
