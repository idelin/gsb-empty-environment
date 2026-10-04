# roaring — 压缩位图 + 无锁并发队列 + CAS 加速（Go）

高性能压缩位图实现：紧凑存储、高效布尔集合运算，三种容器自动转换，
读路径完全无锁，写路径以 CAS 自旋锁做容器级细粒度同步（无全局互斥锁），
并提供基于无锁 MPMC 队列的并行批量 AND/OR/XOR。

## 特性

- **三种容器自动转换**
  - `ArrayContainer`：有序 uint16 数组，基数 ≤ 4096 时使用
  - `BitmapContainer`：固定 65536 位（8 KiB）位图，稠密数据
  - `RunContainer`：RLE 区间编码，连续区间（如 `[0,65535]` 仅占 4 字节）
  - 每次写入/运算后按序列化字节数自动选择最紧凑表示（`normalize`）
- **无全局锁的并发设计**
  - 65536 个容器槽位通过 `atomic.CompareAndSwapPointer` 惰性安装
  - 已发布容器不可变，写者 clone-modify-publish（写时复制），读者零锁
  - 写者之间仅按容器粒度以 CAS 自旋锁互斥，不同分片完全并行
- **无锁 MPMC 队列**（`LFQueue`，Vyukov 有界队列）：每槽位序列号 +
  单次 CAS，head/tail 缓存行填充，~50 ns/op
- **并行集合运算**：`ParallelAnd/Or/Xor` 将容器任务经无锁队列分发给
  worker 池，每个结果槽位单写者，无需额外同步

## 快速开始

```bash
go test ./...          # 单元 + 并发 + 差分测试
go test -race ./...    # 数据竞争检测
go test -bench .       # 基准
go run ./example       # 运行样例
```

## API 一览

```go
b := roaring.New()
b.Add(42)                    // 稀疏离散 ID
b.AddMany([]uint32{...})     // 批量（排序后按容器分组，单次克隆）
b.AddRange(1000000, 1999999) // 密集连续区间
b.Contains(42)               // 无锁读
b.Remove(42)
b.Cardinality()
b.Optimize()                 // 全部容器重写为最紧凑表示
and, or, xor := a.And(b), a.Or(b), a.Xor(b)
p := a.ParallelAnd(b, 8)     // 无锁队列驱动的并行版本
q := roaring.NewLFQueue(1024) // 无锁 MPMC 队列
q.Push(v); v, ok := q.Pop()
```

## 运行样例输出（`go run ./example`）

```
sparse: cardinality=199993 stats={NumContainers:62457 NumArray:62457 NumBitmap:0 NumRun:0 Cardinality:199993}
dense : cardinality=15000003 stats={NumContainers:234 NumArray:2 NumBitmap:0 NumRun:232 Cardinality:15000003}
AND  (33554432 results) took 435µs
OR   (100663296 results) took 770.625µs
XOR  (67108864 results) took 1.163ms
ParallelAND(8 workers) took 1.051417ms, matches=true
queue: drained 100 items, sum=4950 (want 4950)
```

稀疏 ID 自动落入 Array 容器；连续区间自动压缩为 Run 容器
（1500 万 ID 仅占 232 个 run 容器）。两个各约 6700 万 ID 的位图
求交约 **435µs**，求约 **770µs**，达到微秒级。

## 测试结果

```
$ go test -count=1 -timeout 600s .
ok  	github.com/example/roaring	4.318s
$ go test -race -count=1 -timeout 600s .
ok  	github.com/example/roaring	192.205s   # 无数据竞争
```

基准（darwin/amd64）：

```
BenchmarkAddSparse-10            3000000     560 ns/op
BenchmarkAddRangeDense-10         500000    3319 ns/op   # 1M 连续 ID/次
BenchmarkContains-10           500000000     2.90 ns/op  # 无锁读
BenchmarkAndDense-10                3000  437592 ns/op   # 2×6700万 ID 求交
BenchmarkOrDense-10                 2000  951017 ns/op
BenchmarkXorDense-10                1000 1169431 ns/op
BenchmarkParallelAndDense-10        3000  832391 ns/op
BenchmarkQueuePushPop-10        20000000    64.0 ns/op
BenchmarkQueueMPMC-10           30000000    51.6 ns/op
```

## 测试覆盖

- **容器层**：array→bitmap 阈值转换、bitmap→array 降级、RLE 自动选择、
  全 16 位边界（0 / 65535 / 满区间）、run 合并/拆分/桥接、
  三种容器两两 AND/OR/XOR 对布尔集合参考实现的随机差分测试
- **位图层**：稀疏/密集插入、与 `map[uint32]bool` 参考实现的集合运算
  差分、并行与串行结果一致性、多写者同容器 CAS 压力、读写并发（-race）
- **队列层**：容量取整、空 Pop、满 Push、FIFO 顺序、回绕复用、
  8 生产者 × 8 消费者 exactly-once 校验、SPSC 百万级压力
- **无泄漏**：并行运算与队列压测前后断言 goroutine 数回到基线
