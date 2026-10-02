# cbitmap — 压缩位图（无锁并发 + CAS 加速，Go 标准库实现）

Roaring 风格的压缩位图：紧凑存储 + 高效布尔集合运算，全程无全局互斥锁，
基于原子操作与无锁数据结构实现高吞吐并发。

## 设计

- **三类容器自动转换**（每个容器存储同一高 16 位的低 16 位值集合）
  - `ArrayContainer`：有序数组，基数 ≤ 4096 时最省空间
  - `BitmapContainer`：65536 位（8KiB）位图，稠密且不适合 RLE 时使用
  - `RunContainer`：RLE 区间编码，连续区间（如 `AddRange` 写入）最省空间
  - 每次写入/集合运算后按空间最优原则自动选择表示
- **无锁并发**
  - 两级原子指针表（256×256）定位容器槽位，叶表按需 CAS 分配
  - 容器发布后不可变，写路径 copy-on-write + CAS 重试，读路径完全无锁
  - 无任何全局互斥锁
- **批量集合运算 AND / OR / XOR**
  - 容器级快路径：bitmap-bitmap 按字运算、run-run 区间合并/扫描、run-bitmap 直接转换
  - 位图级并行：任务经 Michael-Scott 无锁 MPMC 队列分发到 worker 池

## 运行

```sh
go test -race ./...        # 全部测试（含并发安全与 goroutine 泄漏断言）
go test -run '^$' -bench . # 基准测试
go run ./cmd/demo          # 运行样例
```

## 测试结果（Apple M1 Pro, go1.24）

```
ok  	cbitmap	4.2s      # go test -race ./... 全部通过
```

基准（每个位图 1024 个全满 64K 块 ≈ 6710 万元素，模拟海量数据）：

```
BenchmarkAndDense-10            100    282099 ns/op   # 交集 ~0.28ms
BenchmarkOrDense-10             100    261131 ns/op   # 并集 ~0.26ms
BenchmarkXorDense-10            100    241637 ns/op   # 对称差 ~0.24ms
BenchmarkConcurrentAdd-10       100       727 ns/op   # 并发单点插入
BenchmarkQueueEnqueueDequeue-10 100       103 ns/op   # 无锁队列入/出队
```

样例输出（`go run ./cmd/demo`）：

```
bitmap A: {Containers:917 Array:763 Bitmap:0 Run:154 Cardinality:10099906}
bitmap B: {Containers:993 Array:763 Bitmap:0 Run:230 Cardinality:15099905}
AND: 5000179 values in 1.4665ms
OR : 20199632 values in 631.875µs
XOR: 15199453 values in 656.125µs

large-scale: 67108864 values per bitmap
AND: 67108864 values in 328.875µs
OR : 67108864 values in 338.334µs
```

## 文件结构

- `container.go` — 三类容器及其自动转换、容器级 AND/OR/XOR 快路径
- `bitmap.go` — 无锁 Bitmap（CAS 槽位、Add/AddRange/Contains、并行集合运算）
- `queue.go` — Michael-Scott 无锁 MPMC 队列（泛型）
- `bitmap_test.go` / `queue_test.go` — 边界、并发安全（`-race`）、泄漏断言、基准
- `cmd/demo/main.go` — 运行样例
