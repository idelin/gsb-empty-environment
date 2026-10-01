# sacksim

离散事件模型中的可靠字节流传输模拟：发送方维护已发送字节区间与 SACK
集合，超时仅重传缺口（gap），支持 ACK 乱序、重叠 SACK、窗口收缩；序号
使用扩展整数（任意精度，不回绕）。所有包、队列与链路都运行在整数 tick
的离散事件调度器上，不产生任何真实网络流量。仅使用 Go 标准库。

## 结构

- `sim/scheduler.go` — 离散事件调度器：整数 tick，同 tick 内 FIFO。
- `sim/seq.go` — `Seq`：基于 `math/big.Int` 的扩展序号（可跨越 2^64）。
- `sim/sack.go` — `SackSet`：有序、互不相交、自动合并的字节区间集合；
  重叠/相邻区间并集只计一次；`Gaps(lo,hi)` 求已确认集合的补集（缺口）。
- `sim/link.go` — `Pipe`：单向链路，延迟为整数 tick，支持确定性丢包
  策略；`Packet`/`Endpoint` 定义。
- `sim/sender.go` — 发送方：跟踪 `[sndUna, sndNxt)` 已发送区间，维护
  SACK 并集；拒绝确认未发送字节的 ACK（累计确认或 SACK 块越界均拒绝）；
  旧 ACK 不回退 `sndUna`、不触发已确认数据重传；超时按缺口重传；
  运行时支持 `SetCwnd` 收缩/扩张与接收方通告窗口 `Rwnd`。
- `sim/receiver.go` — 接收方：乱序缓存、按序交付，回复累计 ACK +
  SACK 块；容忍部分重叠的重传段。
- `cmd/demo` — 运行样例。

## 运行样例

```
$ go run ./cmd/demo
[tick     0] SND SEND [4611686018427387904,4611686018427388904) 1000B
...
[tick    20] SND CWND -> 3000
[tick    25] SND RETX [4611686018427393904,4611686018427394904) 1000B (gap retransmit)
[tick    25] SND RETX [4611686018427400904,4611686018427401904) 1000B (gap retransmit)
...
---- result ----
delivered:        20000/20000 bytes, in-order match: true
final tick:       200
sent (new):       20000 bytes
retransmitted:    3000 bytes (gaps only)
data pkts dropped: 3, ack pkts dropped: 1
timeouts:         2
stale ACKs:       0, rejected ACKs: 0
duplicate bytes at receiver: 0
acked union (SACK set): 20000 bytes
```

样例中序号起点为 2^62（扩展整数），链路每第 7 个数据包、每第 11 个
ACK 丢弃，tick 20 时窗口由 8000 收缩到 3000。3 个丢失的数据包恰好
重传 3000 字节——只重传缺口，接收方零重复字节。

## 测试结果

```
$ go test ./... -v
=== RUN   TestEndToEndLossyLink
--- PASS: TestEndToEndLossyLink (0.00s)
=== RUN   TestEndToEndAckLossAndDupAcks
--- PASS: TestEndToEndAckLossAndDupAcks (0.00s)
=== RUN   TestEndToEndWindowShrinkMidTransfer
--- PASS: TestEndToEndWindowShrinkMidTransfer (0.00s)
=== RUN   TestSchedulerTickOrder
--- PASS: TestSchedulerTickOrder (0.00s)
=== RUN   TestSchedulerMaxTick
--- PASS: TestSchedulerMaxTick (0.00s)
=== RUN   TestSchedulerCascadingEvents
--- PASS: TestSchedulerCascadingEvents (0.00s)
=== RUN   TestRejectAckOfUnsentBytes
--- PASS: TestRejectAckOfUnsentBytes (0.00s)
=== RUN   TestStaleAckDoesNotRetransmit
--- PASS: TestStaleAckDoesNotRetransmit (0.00s)
=== RUN   TestOutOfOrderAndOverlappingAcks
--- PASS: TestOutOfOrderAndOverlappingAcks (0.00s)
=== RUN   TestCongestionWindowShrink
--- PASS: TestCongestionWindowShrink (0.00s)
=== RUN   TestAdvertisedWindowShrink
--- PASS: TestAdvertisedWindowShrink (0.00s)
=== RUN   TestSeqExtendedPrecision
--- PASS: TestSeqExtendedPrecision (0.00s)
=== RUN   TestSackSetOverlapCountedOnce
--- PASS: TestSackSetOverlapCountedOnce (0.00s)
=== RUN   TestSackSetGaps
--- PASS: TestSackSetGaps (0.00s)
PASS
ok  	sacksim/sim
```

覆盖的关键性质：

- 重叠 SACK 并集只计一次（`TestSackSetOverlapCountedOnce`）。
- 确认未发送字节的 ACK 被拒绝（`TestRejectAckOfUnsentBytes`）。
- 旧 ACK 不使已确认数据重传（`TestStaleAckDoesNotRetransmit`）。
- ACK 乱序 + 重叠 SACK 合并（`TestOutOfOrderAndOverlappingAcks`）。
- 拥塞窗口与通告窗口收缩（`TestCongestionWindowShrink`、
  `TestAdvertisedWindowShrink`、端到端 `TestEndToEndWindowShrinkMidTransfer`）。
- 扩展整数序号跨越 2^64 边界（`TestSeqExtendedPrecision`，端到端测试
  的序号起点为 2^64-500）。
- 有损链路端到端传输：重传字节数恰好等于丢失字节数，接收方零重复
  （`TestEndToEndLossyLink`）；ACK 丢失/重复时仍能正确完成
  （`TestEndToEndAckLossAndDupAcks`）。
