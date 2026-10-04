# bitmap — 压缩位图 · 多节点并发竞争与冲突仲裁器 (Go)

Roaring 风格的压缩位图，仅使用 Go 标准库。支持稀疏离散 ID 与密集连续区间
ID 的紧凑存储、三种容器自动转换、AND/OR/XOR 批量集合运算，并在多执行体
（goroutine）并发竞争下保证原子性、线性一致性、无死锁、无竞态。

## 结构

```
bitmap.go         Bitmap：256 分片 + 冲突仲裁器（乐观快照 / 有序全锁回退）
container.go      Array / Bitmap / RLE 三种容器与自动转换
container_ops.go  容器级 AND/OR/XOR（纯函数，结果自动选最优表示）
ops.go            位图级批量运算 And/Or/Xor/AndMany/OrMany
cmd/demo/         可运行示例 (go run ./cmd/demo)
*_test.go         20 个测试：参考模型对照、边界、并发安全、泄漏断言、基准
```

## 容器自动转换

| 容器             | 表示                          | 触发条件                                  |
|------------------|-------------------------------|-------------------------------------------|
| `arrayContainer` | 有序 `[]uint16`               | 基数 ≤ 4096                                |
| `bitmapContainer`| 固定 8 KiB 位图 (1024 uint64) | 基数 > 4096 且 RLE 不更省                  |
| `runContainer`   | 有序不相交区间 `[start,last]` | 区间编码字节数 < 数组/位图（密集连续区间） |

每次写入与每次集合运算后都会执行 `optimize`，按实际字节数选择最省表示；
例如 `AddRange` 产生的连续区间自动成为 RLE 容器（单个区间仅 4 字节即可
覆盖 65536 个 ID）。

## 并发模型（冲突仲裁器）

- 键空间按高 16 位划分为 256 个分片，每分片一把 `sync.RWMutex`；
  点操作（`Add`/`Remove`/`Contains`）只触一个分片，天然低竞争。
- 每次成功修改递增原子版本号；每个曾有容器的分片在原子 occupancy mask
  中置位（只增不清，且先于插入可见）。
- 批量运算两级仲裁：
  1. **乐观快照（seqlock 式）**：读版本号与 occupancy mask，仅读锁
     “可能有数据”的分片（全局统一顺序：位图 id → 分片序号），计算后
     校验版本号未变。未变即证明窗口内所有分片恒定，结果等价于某一
     原子时刻的快照 —— 线性一致。小位图只锁少量分片，微秒级完成。
  2. **有序全锁回退**：写竞争下校验持续失败时，按同一全局顺序读锁
     所有相关位图的所有分片。所有 goroutine 加锁顺序一致、写者从不
     同时持有两把锁，因此构造上无死锁且必然终止。
- 批量运算不修改操作数，结果独立分配。

## 使用

```go
a := bitmap.New()
a.Add(7)                      // 稀疏离散 ID
a.AddRange(1000, 20000)       // 密集连续区间 -> 自动 RLE 容器

b := bitmap.New()
b.AddRange(1500, 3000)

fmt.Println(bitmap.And(a, b).Cardinality()) // 交集
fmt.Println(bitmap.Or(a, b).Cardinality())  // 并集
fmt.Println(bitmap.Xor(a, b).Cardinality()) // 对称差
bitmap.AndMany(a, b, c)                     // 多位图批量交
bitmap.OrMany(a, b, c)                      // 多位图批量并
```

## 运行样例

```
$ go run ./cmd/demo
a: 1000002 ids
b: 1500001 ids
AND: 500001
OR:  2000002
XOR: 1500001
a containers: arrays=1 bitmaps=0 runs=16
concurrent load: 1600000 ids in 155.171834ms
AND over 10^9-id dense bitmaps: 500000000 ids in 1.038583ms
```

## 测试结果

```
$ go test -race -count=1 ./...
ok      bitmap  27.395s
?       bitmap/cmd/demo [no test files]
```

20 个测试全部在 `-race` 下通过，覆盖：

- 容器转换边界（4096/4097 阈值、满容器、uint16/uint32 边界、跨容器区间）
- 随机混合负载 vs 朴素 map 参考模型（4000 步 + 全量校验）
- AND/OR/XOR/AndMany/OrMany vs 集合论参考结果 + 代数恒等式
- 并发：16 写者离散/重叠写入零丢失更新、增删竞争一致性、
  读写与批量运算混合竞争、四种操作数排列的批量运算死锁探测（超时熔断）、
  单调负载下批量结果的线性一致性子集断言
- 严格无泄漏：工作负载平息后 goroutine 数必须回到基线

## 基准（Apple M1 Pro，机器高负载 load avg≈300 下测得，空闲时更快）

```
BenchmarkContainerAndDense-10     8587 ns/op     位图容器级 AND（1024 个 uint64）
BenchmarkContainerAndRun-10         52 ns/op     RLE 容器级 AND（O(区间数)）
BenchmarkAndFewRanges-10         70069 ns/op     4.3e9 ID 空间、少量密集区间的 AND
BenchmarkOrFewRanges-10          13299 ns/op     同上，OR
BenchmarkAndDense100M-10        192486 ns/op     两个 10^8 bit 稠密位图的 AND
BenchmarkAndFullSpace-10       26047356 ns/op    整个 2^32 空间（65536 容器）的 AND
BenchmarkAndDense10B-10        83127040 ns/op    10^10 bit（百亿）容器级模拟 AND
BenchmarkAndSparseRandom-10     1036302 ns/op    随机稠密位图容器 AND（最坏情况）
BenchmarkAddSparse-10              381 ns/op     单点插入
BenchmarkContains-10                17 ns/op     单点查询
```

要点：可压缩负载（少量密集区间，ID 空间达 43 亿）下两个位图的交并运算
为微秒级；百亿级数据量以等价的 152588 个 RLE 容器对模拟，单容器对交运算
约 52 ns，总耗时随容器数线性伸缩。
