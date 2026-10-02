# fibersched — 协作式轻量级协程（Fiber）调度器

单线程、单进程、离散事件的协作式协程调度器，Go 标准库实现，轮转（Round-Robin）调度，事件序列完全确定、可复现。

## 设计

- **协作式切换**：Fiber 通过 `Yield()` 主动让出；函数返回即退出（`EXIT`）。
- **轮转调度**：就绪队列 FIFO，`Yield` 的协程排到队尾。
- **确定性**：任意时刻只有一个 fiber 在运行；调度器与 fiber 之间通过无缓冲 channel 做严格往返握手（resume → yield/exit），无定时器、无随机、无抢占。每个 fiber 的 goroutine 在 `Spawn` 时创建一次并立即驻车，`resume` channel 永远至多一个等待者。因此 tick 日志是程序的纯函数，运行任意多次逐字节一致。
- **逻辑时钟**：每个事件（SPAWN/RESUME/YIELD/EXIT）tick 严格 +1，稠密无空洞。

## 运行样例

```sh
go run ./cmd/demo
```

输出（每次运行完全相同）：

```
tick=0000 fiber=0(A) event=SPAWN
tick=0001 fiber=1(B) event=SPAWN
tick=0002 fiber=2(C) event=SPAWN
tick=0003 fiber=0(A) event=RESUME
tick=0004 fiber=0(A) event=YIELD
tick=0005 fiber=1(B) event=RESUME
tick=0006 fiber=1(B) event=YIELD
tick=0007 fiber=2(C) event=RESUME
tick=0008 fiber=2(C) event=YIELD
...
total ticks: 27
```

## 测试

```sh
go test ./fiber/ -v
go test ./fiber/ -race      # 并发握手正确性
```

覆盖：3 协程严格交替（A,B,C 轮转）、完整事件序列形状、200 次重复运行日志逐字节一致（消除并发不确定性）、fiber 内动态 Spawn、空调度器立即终止。
