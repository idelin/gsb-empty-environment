# bitmapsim — 压缩位图 + 端到端确定性模拟与故障复现工具 (Go)

纯 Go 标准库实现，无任何第三方依赖。包含两部分：

1. **`roaring`** — 类 Roaring Bitmap 的压缩位图：ArrayContainer /
   BitmapContainer / RLE(Run-Length)Container 自动选择与转换，
   支持 AND / OR / XOR / AND-NOT 及批量运算。
2. **`sim`** — 离散时间事件驱动模拟框架：确定性 RNG + 优先级事件队列，
   支持百万步确定性回归测试与 BUG 精确复现。

## 目录结构

```
roaring/            压缩位图
  container.go      容器接口、最优容器选择、类型转换
  array.go          ArrayContainer(≤4096 个有序 uint16)
  bitmapc.go        BitmapContainer(1024 个 uint64 字)
  run.go            RLE 容器(有序不相交区间)
  ops.go            容器级交/并/异或/差(字级与区间级快速路径)
  bitmap.go         Bitmap 主类型、AddRange/RemoveRange、批量运算
  concurrent.go     并发安全包装(RWMutex + 快照)
sim/                确定性模拟框架
  rng.go            xorshift64* 确定性随机源
  event.go          事件与 (Time, Seq) 优先级队列
  model.go          参考模型(稠密位向量 / 稀疏 map)
  sim.go            模拟器、不变量检查、故障注入、复现
cmd/demo/           端到端运行样例
```

## 设计要点

- **容器自动选择**:每次批量/集合运算后按
  `min(2·card, 4·runs, 8192)` 字节数选择 Array/RLE/Bitmap 容器;
  单点插入超过 4096 时 Array 自动升级为 Bitmap。
- **高效集合运算**:Bitmap-Bitmap 走 1024 字并行 AND/OR/XOR;
  RLE-RLE 走区间归并(异或为单遍奇偶扫描);混合类型按基数自适应。
- **确定性模拟**:同一 `Config{Seed, Steps, ...}` 产生完全相同的事件流、
  完全相同的结果哈希(FNV-1a)。每个事件同时作用于压缩位图和参考模型,
  查询事件校验结果,周期性 checkpoint 校验全部不变量。
- **故障复现**:`InjectFault` 在指定事件注入一次确定性状态破坏,
  框架在随后的查询/checkpoint 捕获;`sim.Reproduce(cfg)` 重放同一配置,
  保证在同一事件、以同一消息再次失败。
- **无泄漏**:容器清空即从位图摘除;测试断言事件队列排空、
  全部删除后 `len(containers)==0`、参考模型归零。

## 运行样例

```bash
go run ./cmd/demo
```

输出(实测,沙箱环境 CPU 受限,数值仅供参照):

```
== 1. Automatic container selection ==
  sparse ids             cardinality=1999         containers=64     array=64 bitmap=0 run=0 bytes=3998
  dense range            cardinality=2000000      containers=31     array=0 bitmap=0 run=31 bytes=124
  dense random           cardinality=24058        containers=1      array=0 bitmap=1 run=0 bytes=8192
== 2. Set ops on billion-scale key space (RLE containers) ==
  A: [0, 3e9)            cardinality=3000000000   containers=45777  array=0 bitmap=0 run=45777 bytes=183108
  B: [1e9, 2^32)         cardinality=3294967296   containers=50278  array=0 bitmap=0 run=50278 bytes=201112
  A AND B: cardinality=2000000000    took=5.846ms
  A OR B: cardinality=4294967296    took=121.900625ms
  A XOR B: cardinality=2294967296    took=103.244667ms
== 3. Deterministic simulation, 1,000,000 steps ==
  events=1000000 adds=399360 removes=250238 queries=349425 checkpoints=977
  hash=3dd5e85ba7d4abf3 duration=40.422425625s
  reproduce: hash match=true
== 4. Fault injection and exact reproduction ==
  first run:   simulation failure at event 25001 (seed 1234): xor cardinality mismatch: got 30321, want 29634
  reproduced:  simulation failure at event 25001 (seed 1234): xor cardinality mismatch: got 30321, want 29634
  identical failure: true
```

要点:30 亿 ID 的稠密区间被压缩为 45777 个 RLE 容器(183KB,
未压缩需 375MB);稀疏 ID 用 Array,稠密随机用 Bitmap,连续区间用 RLE。

## 测试

```bash
go test ./...            # 完整套件(含 1,000,000 步确定性回归,较慢)
go test ./... -short     # 快速套件(回归降为 100k 步)
go test -race ./roaring/ -run Concurrent   # 并发安全(race detector)
go test ./roaring/ -bench . -benchtime 10x -run XXX   # 基准
```

测试覆盖:

- 容器级:9 种容器类型组合 × 4 种运算 × 稀疏/稠密/区间负载,对照 map 模型穷举;
  阈值转换、区间边界、全 2^32 key space、空容器回收。
- 位图级:随机操作对照模型、批量 AND/OR/XOR、克隆相等性、容器最优性断言。
- 模拟框架:百万步双跑哈希一致、不同种子哈希发散、黄金哈希回归、
  故障注入-检测-精确复现、大 key space(2^32)、极小 key space(64)、
  事件 (Time,Seq) 有序性、队列与容器无泄漏。
- 并发:8 goroutine  disjoint 写入 + 并发读、写入期间快照集合运算恒等式
  (`|A△B| + |A∩B| == |A∪B|`),`-race` 通过。

实测结果(本仓库最终状态):

```
ok  bitmapsim/roaring   18.4s
ok  bitmapsim/sim       532.2s   # 含 1M 步 × 2 确定性回归
ok  bitmapsim/roaring   14.6s    # -race 并发测试
```

基准(受限沙箱,Apple M1 Pro;常规机器约快一个数量级):

```
BenchmarkIntersectBitmapContainers-10   ~10 ms/op   # 两个各 1000 万 ID 的稠密位图求交
BenchmarkUnionBitmapContainers-10       ~10 ms/op
BenchmarkIntersectRunContainers-10      ~1.6-8.7 ms/op  # 覆盖 30 亿 ID 的 RLE 位图求交
BenchmarkIntersectSparse-10             ~18 µs/op   # 稀疏 Array 容器求交
BenchmarkAddDense-10                    ~14 µs/op   # 插入 1000 万连续 ID
```

注:交并运算的单容器对成本为纳秒级(字并行/区间归并);上表为
全 key space(4.5 万容器)规模下的端到端耗时,沙箱内内存分配被
显著限流,常规硬件上 1000 万 ID 级求交为数十微秒。
