# ringpool — 定长环形内存池与零拷贝切片复用器

面向高性能网络 IO 的内存缓冲区管理器，仅使用 Go 标准库。

## 设计

- **定长块环形缓冲区**：池在创建时一次性分配 `numBlocks × blockSize`
  字节的常驻内存（默认配置 256 × 16KiB = 4MiB），之后永不增长。
- **零拷贝只读切片**：`Write` 把字节流拷入环中（唯一的一次拷贝）；
  `Slice(max)` 直接从环内存切出只读 `Slice` 视图，不做任何内存拷贝。
  切片最多覆盖单个块的未读剩余，跨块数据由调用方循环切取。
- **引用计数安全回收**：每个块记录 `refs`。切片 `Release` 时计数减一；
  当块已被消费者读完（retired）且计数归零，块立即回到空闲栈复用。
  多块切片引用同一块时，块直到最后一个引用释放才回收。
- **背压**：环满时 `Write` 阻塞，直到消费者释放切片腾出块；
  无数据时 `Slice` 阻塞。`Close` 唤醒所有阻塞方。
- **GC 开销基本为零**：块内存、待读索引队列、空闲栈全部预分配；
  `Slice` 头部对象用 `sync.Pool` 复用。稳态热路径堆分配为 0
  （基准：`0 B/op, 0 allocs/op`）。

## API

```go
pool := ringpool.New(256, 16*1024) // 4MiB 常驻环形池
go func() {
    pool.Write(frame)   // 生产者：拷入环，环满则阻塞
    pool.Close()
}()
for {
    s, err := pool.Slice(4096) // 消费者：零拷贝切出只读视图
    if err == ringpool.ErrClosed { break }
    use(s.Bytes())             // 视图直接引用环内存
    s.Release()                // 引用计数归零后块安全回收
}
```

注意：`Slice` 及其 `Bytes()` 视图在 `Release` 之后不得再使用，
底层内存会被复用。

## 运行样例

```
$ go run ./cmd/demo
ring pool: 256 blocks x 16 KiB = 4 MiB resident
streamed:   100 MiB in 2.196s (46 MiB/s)
slices:     12677 borrowed / released (zero-copy)
pool:       leased=0 free=256/256 buffered=0
runtime:    heapAlloc=4454 KiB numGC=1
OK: borrow/release conserved, no leaks
```

（demo 吞吐包含测试侧确定性随机帧生成开销；池本身热路径见基准。）

## 测试结果

```
$ go test -v ./...
--- PASS: TestStream100MB (2.44s)
    streamed 100 MiB through 4 MiB ring; slices borrowed=12677 released=12677; leased=0 free=256/256
--- PASS: TestSliceZeroCopy      # 切片直接别名环内存，共享块延迟回收
--- PASS: TestBackpressure       # 环满写阻塞，释放切片后解除
--- PASS: TestSteadyStateAllocs
    32MiB steady-state: mallocs=5 allocBytes=1400 numGC=0
--- PASS: Example

$ go test -race ./...        # 竞态检测全部通过
$ go test -bench . -benchtime 2s .
BenchmarkSlice-10  300000  7496 ns/op  2185.63 MB/s  0 B/op  0 allocs/op
```

验收边界对应：

| 要求 | 验证 |
| --- | --- |
| 连续流入 100MB 随机帧流 | `TestStream100MB`：100MiB 随机帧逐字节校验 |
| 内存池常驻仅 4MB | 池固定 256×16KiB，运行时堆占用 ≈4.5MB |
| 借用与释放严格守恒 | borrowed == released，drain 后 `leased=0`、`free=256/256` |
| 无内存泄漏、GC 开销基本为零 | 稳态 32MiB 仅 5 次分配/1400B、0 次 GC；基准 0 allocs/op |

## 文件

- `pool.go` — 环形内存池与零拷贝切片实现
- `pool_test.go` — 100MB 流完整性/守恒/背压/零分配测试与基准
- `example_test.go` — 可运行的 `go test` 示例
- `cmd/demo/main.go` — 100MB 随机帧流演示程序
