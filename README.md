# corochan

Go 协程同步原语与无锁环形队列，仅使用标准库。

## 组件

- **`Channel[T]`**（`channel.go`）：仿 Go channel 语义的协程同步原语。
  - 带缓冲 / 无缓冲（`NewChannel[T](capacity)`，`capacity == 0` 为无缓冲 rendezvous）。
  - 通道满（发送方）或空（接收方）时自动挂起当前协程。
  - 精确唤醒：每个挂起的协程持有独立的 buffered(1) 门铃 channel，对端只唤醒
    匹配的那一个协程（FIFO 顺序，无惊群）。
  - `Close` 广播：唤醒所有等待读协程并返回 EOF（`ok == false`）；等待写协程按
    Go 语义 panic（`send on closed channel`）。
  - 附带 `TrySend` / `TryRecv` / `Len` / `Closed`。
- **`RingQueue[T]`**（`ringqueue.go`）：有界、无锁 MPMC 环形队列
  （Dmitry Vyukov 协议，每槽位序列号 + CAS），满/空时返回错误而非阻塞。
  `Channel` 的缓冲即构建于其上（通道内以精确计数保证容量语义）。

## 运行

```sh
go test -race -v ./...   # 全部测试（含竞态检测）
go run ./cmd/demo        # 验收场景运行样例
```

## 验收场景（`TestAcceptance_BufferedWakeClose` / demo 输出）

1. 协程 P 向容量为 2 的 Channel 写入 3 个数据，第 3 次 `Send` 自动挂起；
2. 消费者 C 读取 1 个数据后，P 被精确唤醒并完成写入；
3. `Close` 后所有等待读协程收到 EOF。

```
[   0.0ms] P: Send(1) returned
[   0.1ms] P: Send(2) returned
[ 100.9ms] main: buffer holds 2 values; P parked on 3rd send = true
[ 101.3ms] C: Recv() = 1 (frees a slot, waking P)
[ 101.3ms] P: Send(3) returned
[ 204.1ms] main: closing channel
[ 204.2ms] R1: Recv() => EOF (channel closed)
[ 204.2ms] R2: Recv() => EOF (channel closed)
```

## 测试结果（go test -race，Go 1.24）

```
--- PASS: TestAcceptance_BufferedBlockWakeClose (0.15s)
--- PASS: TestUnbufferedRendezvous (0.11s)
--- PASS: TestPreciseWakeup (0.20s)
--- PASS: TestSendOnClosedChannelPanics (0.08s)
--- PASS: TestDoubleClosePanics (0.00s)
--- PASS: TestFIFOOrder (0.00s)
--- PASS: TestTrySendTryRecv (0.00s)
--- PASS: TestChannelStress (0.07s)
--- PASS: TestRingQueueBasic (0.00s)
--- PASS: TestRingQueueWraparound (0.00s)
--- PASS: TestRingQueueConcurrent (0.03s)
PASS
ok  	corochan
```
