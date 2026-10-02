# coroutine：协程通道（Channel）同步原语 + 无锁环形队列

纯 Go 标准库实现，无任何第三方依赖。

## 组件

### 1. Channel —— 仿 Go channel 语义的协程同步原语（`channel.go`）

- **带缓冲 / 无缓冲**：`New(0)` 为无缓冲（rendezvous）通道，`New(n)` 为容量 n 的缓冲通道。
- **自动挂起**：通道满时 `Send` 挂起当前协程；通道空时 `Recv` 挂起当前协程。
  挂起通过每个等待者独立的 `chan struct{}` 信号实现（`close` 即唤醒）。
- **精确唤醒**：发送者与接收者各自维护 FIFO 等待队列；对端操作到来时
  只唤醒队首的匹配协程，数据直接交接（无缓冲时不过缓冲，缓冲通道中
  `Recv` 释放槽位后由队首发送者原地补入）。
- **关闭广播**：`Close` 唤醒所有挂起协程——等待读的协程收到 EOF
  （`Recv` 返回 `(nil, false)`），等待写的协程 panic（与 Go 一致）；
  关闭后缓冲中的数据仍可读出，耗尽后才返回 EOF。

```go
ch := coroutine.New(2) // 容量 2 的缓冲通道
go func() {
    ch.Send(1); ch.Send(2); ch.Send(3) // 第 3 次写入时挂起
}()
v, ok := ch.Recv() // 读出 1，同时精确唤醒挂起的生产者完成 Send(3)
ch.Close()         // 广播：所有等待读的协程收到 ok == false（EOF）
```

### 2. RingQueue —— 无锁有界 MPMC 环形队列（`ringqueue.go`）

基于 Dmitry Vyukov 的 bounded MPMC queue 算法：头尾指针与每个槽位的
序列号均通过 CAS（`sync/atomic`）推进，全程无互斥锁。容量向上取整为
2 的幂；`Enqueue` 满时、`Dequeue` 空时立即返回 `false`（非阻塞），
多生产者多消费者并发安全。

## 运行样例

```sh
go run ./cmd/demo
```

输出（时间戳清晰展示 P 在第 3 次写入挂起、C 读取后 P 被立即唤醒）：

```
[   0.1ms] P: Send(3) 开始
[ 101.6ms] C: Recv() -> 读取一个数据，唤醒挂起的 P
[ 101.7ms] C: 收到 1
[ 101.7ms] P: Send(3) 完成
...
[ 203.4ms] main: Close() 广播
[ 203.5ms] R2: 收到 EOF
[ 203.5ms] R3: 收到 EOF
[ 203.5ms] R1: 收到 EOF
...
[ 203.5ms] 无锁队列消费 8 个元素，校验和 = 60（期望 1+2+3+4+11+12+13+14 = 60）
```

## 测试

```sh
go test -race -v ./...
```

测试结果（Go 1.24.2，开启竞态检测，全部通过）：

```
--- PASS: TestAcceptanceScenario        (0.10s)  # 验收场景：cap=2 写 3 个，第 3 次挂起，读 1 个后精确唤醒
--- PASS: TestCloseBroadcastsEOF        (0.10s)  # 关闭广播：5 个等待读协程全部收到 EOF
--- PASS: TestUnbufferedRendezvous      (0.10s)  # 无缓冲通道 rendezvous 语义
--- PASS: TestPreciseFIFOWakeup         (0.16s)  # 精确唤醒：一次 Recv 只唤醒队首发送者
--- PASS: TestCloseDrainsBufferThenEOF  (0.00s)  # 关闭后先耗尽缓冲再 EOF
--- PASS: TestSendOnClosedPanics        (0.05s)  # 向已关闭通道发送 panic
--- PASS: TestChannelMPMC               (0.01s)  # 多生产者多消费者不丢不重
--- PASS: TestRingQueueFIFO             (0.00s)
--- PASS: TestRingQueueFullAndEmpty     (0.00s)
--- PASS: TestRingQueueWrapAround       (0.01s)  # 槽位环绕复用
--- PASS: TestRingQueueConcurrentMPMC   (0.11s)  # 无锁队列并发压测
PASS
ok      coroutine
```
