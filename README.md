# ringpool — 定长环形内存池与零拷贝切片复用器

高性能网络 IO 内存缓冲区管理器（Go，仅标准库）：

- **定长环形内存池**：池启动时一次性分配 `blockSize × numBlocks` 的扁平字节数组
  （默认 4 KiB × 1024 = 4 MiB），之后不再发生批量分配。
- **零拷贝切片**：`FrameReader` 把流中的帧直接读进环形缓冲区，切出只读
  `Slice` 引用，全程不拷贝载荷字节。
- **引用计数回收**：`Slice` 携带原子引用计数（`Retain`/`Release`），归零时底层
  块安全归还池中复用；双重释放直接 panic，守恒性可机器验证。
- **背压**：环形空间不足时 `Reserve` 阻塞等待消费者释放（`TryReserve` 为非阻塞版本）。
- **GC 开销≈0**：`Reservation`/`Slice` 对象经 `sync.Pool` 复用，热路径
  `0 allocs/op`（见 `TestZeroAlloc` 与基准测试）。

## 结构

| 文件 | 说明 |
|---|---|
| `ringpool.go` | `Pool` / `Reservation` / `Slice`：环形块分配、引用计数、统计 |
| `frame.go` | `FrameWriter` / `FrameReader`：长度前缀帧协议与零拷贝切分 |
| `ringpool_test.go` | 全部验收测试与基准 |
| `cmd/demo/main.go` | 100 MiB 随机帧流端到端演示 |

## 用法

```go
pool := ringpool.New(4096, 1024) // 4 MiB 环形池
defer pool.Close()

// 生产侧：从流中零拷贝切帧
fr := ringpool.NewFrameReader(pool, conn)
for {
    s, err := fr.Next()      // s 为只读切片，引用计数 = 1
    if err == io.EOF { break }
    handle(s.Bytes())        // 直接读取，无拷贝
    s.Release()              // 引用归零，块归还池中
}

// 也可以手动预留/提交
res, _ := pool.Reserve(n)    // 阻塞直到有连续空间
copy(res.Buf, data)
s := res.Commit()
defer s.Release()
```

## 运行样例

```
$ go run ./cmd/demo
streamed        : 100 MiB in 3203 frames
elapsed         : 7.281s (13.7 MiB/s)
checksum        : 13368784477
ring capacity   : 4 MiB (1024 blocks x 4096 B)
max outstanding : 63 KiB (<= ring capacity)
borrow/release  : 3203 / 3203 (conserved: true)
outstanding now : 0 slices, 0 bytes
free blocks     : 1024 / 1024
heap allocs     : 3449 total (1.1 per frame, incl. producer payloads)
```

## 测试结果（Apple M1 Pro, go1.24.2）

```
$ go test ./... -v
=== RUN   TestStream100MB
    ringpool_test.go:117: streamed 104857600 bytes in 3203 frames (checksum=13368784477)
    ringpool_test.go:118: ring capacity 4194304 bytes, max outstanding 65494 bytes
    ringpool_test.go:119: borrowed=3203 released=3203 freeBlocks=1024/1024
--- PASS: TestStream100MB (2.42s)
--- PASS: TestRetainRelease
--- PASS: TestDoubleReleasePanics
--- PASS: TestWrapAround
--- PASS: TestBackpressure
--- PASS: TestTooLarge
--- PASS: TestZeroAlloc
--- PASS: TestFrameReaderCancelOnError
PASS
ok      ringpool    2.859s

$ go test -race ./...          # 竞态检测通过
ok      ringpool    26.756s

$ go test -bench=. -benchtime=2s -run=^$ .
BenchmarkFrameThroughput-10    335070    18158 ns/op    82.61 MB/s    0 B/op    0 allocs/op
```

验收边界核对：

- **100MB 连续随机帧流**：`TestStream100MB` 推送 100 MiB、3203 帧（1 B–64 KiB 随机），逐字节校验通过。
- **常驻 4MB 环形空间**：池仅持有 4 MiB 数组，全程最大在途 63 KiB ≪ 4 MiB。
- **借用/释放严格守恒**：`borrowed == released == 3203`，结束时 `outstanding = 0`、`freeBlocks = 1024/1024`；双重释放 panic 有测试守护。
- **无泄漏、GC 开销≈0**：热路径 `0 allocs/op`（`TestZeroAlloc` + 基准），`go test -race` 通过。
