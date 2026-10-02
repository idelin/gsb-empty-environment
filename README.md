# roaring — 压缩位图（异常注入与崩溃恢复容错性验证，Go 版）

仅使用 Go 标准库实现的 Roaring Bitmap 风格压缩位图，重点覆盖**恶劣工况下的
状态安全回滚与自愈**：模拟突发断电、磁盘满、网络丢包/包损坏，并通过
`go test`（含 `-race`）验证全部边界条件、并发安全性与无 goroutine 泄漏。

## 设计

- **64 位 ID 空间**：ID 拆分为高 48 位 key + 低 16 位 value，同 key 的值存入一个容器。
- **三类容器自动转换**（`container.go`）：
  - `arrayContainer`：有序数组，稀疏数据最优（≤ 4096 个值）；
  - `bitmapContainer`：8 KiB 位图，密集数据最优，AND/OR/XOR 走 64 位字级运算；
  - `runContainer`：RLE 区间编码，连续区间最优（单区间仅 4 字节）。
  - 插入超过阈值自动 array→bitmap；`AddRange` 直接生成 RLE 容器；
    `Optimize()` 按字节数选择最小编码；运算结果自动降级（bitmap→array）。
- **批量集合运算**（`bitmap.go`）：`And/Or/Xor` + `AndBatch/OrBatch/XorBatch`，
  按 key 归并，容器级分发到对应类型的最优实现；全满 RLE 容器有 O(1) 快速路径。
- **崩溃安全持久化**（`storage.go`）：写 `<path>.tmp` → fsync → 备份旧文件为
  `.bak` → 原子 rename 提交。磁盘满回滚临时文件；断电留下半截临时文件但永不提交；
  主文件 CRC 校验失败时自动从 `.bak` 自愈恢复。
- **完整性校验**（`marshal.go`）：魔数 + 全文件 CRC32 + 逐字段边界检查 +
  容器不变量校验（有序性、cardinality 重算比对），损坏数据一律返回 `ErrCorrupt`，绝不 panic。
- **故障注入**（`fault.go`）：`FaultInjector` 支持写入预算（ENOSPC）、断电点、
  提交后主文件位翻转、丢包率、包损坏率、确定性丢前 N 包。
- **丢包网络传输**（`transfer.go`）：1 KiB 分块 + 每块 CRC + 全图 CRC，
  ACK 位掩码 + 选择性重传；`LossyLink` 为内存丢包链路。
- **并发安全**：所有 `Bitmap` 方法持 `sync.RWMutex`；双位图运算按 ID 有序加锁，
  避免 `a.And(b)` 与 `b.And(a)` 并发死锁。

## 运行样例

```bash
go run ./cmd/demo
```

实际输出（Apple M1 Pro，沙箱环境 CPU 有节流，耗时数值仅作量级参考）：

```
=== 1. Container auto-selection ===
sparse (1M random IDs)       cardinality=999879 containers=65536 (array=65536 bitmap=0 run=0) bytes=1999758
dense range [1e7,2e7)        cardinality=10000000 containers=154 (array=0 bitmap=0 run=154) bytes=616
dense random (20k/64k)       cardinality=17257 containers=1 (array=0 bitmap=1 run=0) bytes=8192

=== 2. Set operations ===
AND  sparse∩dense : 67.958µs     cardinality=2308
OR   sparse∪dense : 13.125083ms  cardinality=10997571
XOR  sparse△dense : 101.432917ms cardinality=10995263
AndBatch(3)       : 30.08725ms   cardinality=2308

=== 3. 10^10-scale simulation (RLE containers) ===
A = [0, 5e9)                 cardinality=5000000000 containers=76294 (array=0 bitmap=0 run=76294) bytes=305176
B = [2.5e9, 7.5e9)           cardinality=5000000000 containers=76295 (array=0 bitmap=0 run=76295) bytes=305180
AND  A∩B: 2.084708ms   cardinality=2500000000 (50.8 µs per 1e8 IDs)
OR   A∪B: 15.15175ms   cardinality=7500000000

=== 4. Fault injection & crash recovery ===
committed sparse bitmap (999879 IDs)
[disk full]   save err=roaring: simulated disk full (ENOSPC) | rolled back to 999879 IDs (== sparse: true)
[power loss]  save err=roaring: simulated sudden power loss | recovered 999879 IDs (== sparse: true)
[bit rot]     self-healed=true | restored 999879 IDs (== sparse: true)

=== 5. Lossy-network transfer (30% packet loss) ===
send err=<nil> recv err=<nil> | received 999879 IDs, identical=true
```

要点：10^10 量级下运算代价是 **O(容器数)** 而非 O(基数)——两个各 50 亿 ID 的
位图仅各占约 300 KB（76294 个 RLE 容器），单次交集折合每容器对约 30ns、
每 1e8 ID 约 50µs；稀疏位图交集为微秒级。

## 测试

```bash
go test ./...        # 功能 + 故障注入 + 崩溃恢复 + 并发 + 泄漏断言
go test -race ./...  # 竞态检测
go test -run XXX -bench . -benchmem
```

测试结果：

```
$ go test ./...
ok  	roaring	4.643s
$ go test -race ./...
ok  	roaring	119.736s
```

基准（同一沙箱多次运行波动较大，取典型值）：

```
BenchmarkAnd10B-10                5   ~5-50 ms/op    2911451 B/op    76300 allocs/op   # 5e9 AND 5e9 (RLE)
BenchmarkOr10B-10                 5   ~15-160 ms/op  6882710 B/op   228886 allocs/op   # 5e9 OR 5e9 (RLE)
BenchmarkXor10B-10                5   ~68-90 ms/op   6795092 B/op   190745 allocs/op   # 5e9 XOR 5e9 (RLE)
BenchmarkAndBitmapContainers-10   3   ~0.6-1.7 s/op  2257517 B/op    28662 allocs/op   # 1e7 随机 ID 位图字级 AND
BenchmarkAddSparse-10             5   ~3.5 s/op      6407379 B/op     3603 allocs/op   # 1e6 随机写入（2^24 键域）
```

## 测试覆盖清单

- **容器边界**：0 / 65535 / `math.MaxUint64`；array→bitmap 4096/4097 阈值翻转；
  run 在最小/最大值处的前插、后插、桥接合并；`addInterval` 重叠吞并；
  `Optimize` 三类最小编码选择；跨类型（array×run×bitmap）运算对照参考实现 fuzz。
- **位图运算**：稀疏点 + 密集区间混合数据与 `map[uint64]bool` 参考实现对照；
  空位图、自身运算、批量运算结合律；序列化往返与损坏数据拒绝（截断、位翻转、尾随垃圾）。
- **异常注入与崩溃恢复**：磁盘满回滚（临时文件清除、旧镜像完好）；提交前/后断电
  （半截临时文件不提交、恢复到最后一致镜像、孤儿文件清理）；主文件位翻转/截断后
  从 `.bak` 自愈并回写修复；双副本全毁返回 `ErrCorrupt`；20 连随机偏移断电风暴不丢已提交状态。
- **网络丢包**：0%/30%/40% 丢包、确定性丢前 100 包、25% 包损坏、混合混沌、空位图传输。
- **并发与泄漏**：4 写 4 读并发（`-race`）、双向加锁的并发集合运算、并发 Save/Load、
  严格 goroutine 泄漏断言（基线对比）。

## 已知取舍

- 随机顺序单点 `Add` 在容器/键数组中是 O(n) 插入（经典 Roaring 相同特性），
  批量写入建议有序或使用 `AddRange`。
- `Storage` 保存最新一份完整镜像（`.bak` 为上一份），非增量 WAL。
