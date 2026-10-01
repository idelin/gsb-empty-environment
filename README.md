# tcpsim — 离散事件慢启动 / 拥塞避免 / 超时回退模型

纯标准库实现。所有包、队列与链路都运行在整数 tick 的离散事件调度器上，
**不发送任何真实网络流量**。

## 结构

- `event.go` — 整数 tick 离散事件调度器（二叉堆，同 tick 按入队序，确定性）。
- `link.go` — 单向链路：固定整数时延 + 确定性丢包谓词。
- `sender.go` — 发送方拥塞控制核心。
- `receiver.go` — 累计确认接收方（按序交付，失序丢弃并回重复 ACK）。
- `sim.go` — 仿真装配（调度器 + 双向链路 + 收发双方 + tick 日志）。
- `cmd/demo/main.go` — 可运行样例（16 KB 传输，seq=4000 首次经过时丢失）。
- `sim_test.go` — 全部测试（`go test ./...`）。

## 关键规则与不变式

1. **慢启动**：`cwnd < ssthresh` 时每个新 ACK 使 `cwnd += 1 MSS`（每 RTT 翻倍），
   到达 `ssthresh` 即封顶。
2. **拥塞避免**：`cwnd >= ssthresh` 后每个新 ACK 使 `cwnd += MSS*MSS/cwnd`（加性增）。
3. **超时回退**：`ssthresh = max(cwnd/2, 2*MSS)`，`cwnd = 1 MSS`，
   `RTO *= 2`（上限 `MaxRTO`），从 `sndUna` 回退重传（go-back-N）。
4. **ACK 去重与重传计时分离**：不推进 `sndUna` 的 ACK（重复/旧 ACK）只计数，
   **绝不增大有效窗口，也绝不触碰重传计时器**；只有真实推进的 ACK 才重启计时器
   （计时器用代数计数使旧事件失效）。
5. **累计 ACK 跳跃钳制**：`ackNum > sndNxt` 时钳制到 `sndNxt`，
   只确认真实未确认字节。
6. **窗口非负**：`Inflight()` 与 `Avail()` 恒 `>= 0`，旧 ACK 不会使窗口为负。

## 运行

```sh
go test ./...      # 全部测试
go run ./cmd/demo  # 事件日志样例
```

## 运行样例（节选）

```
tick=0      send: seq=0 len=1000 cwnd=1000
tick=10     ack: ack=1000 newly=1000 sndUna=1000
tick=10     send: seq=1000 len=1000 cwnd=2000        # 慢启动：每 ACK +1 MSS
tick=25     recv: send ack=4000
tick=25     recv: send ack=4000                      # seq=4000 丢失，接收方回重复 ACK
tick=30     ack: dup/old ack=4000 ignored (sndUna=4000)  # 去重：不增窗、不动计时器
tick=70     timeout: backoff cwnd=1000 ssthresh=2500 rto=80 retransmit-from=4000
tick=90     send: seq=7000 len=1000 cwnd=2500        # 进入拥塞避免：加性增
tick=120    send: complete, 16000 bytes acked
---- summary ----
done=true delivered=16000/16000 bytes ticks=200
timeouts=1 dupAcks(sender)=0 dupAcks(receiver)=4 finalCwnd=5193 ssthresh=2500 rto=80
cwnd trace: [1000 2000 3000 4000 5000 1000 2000 2500 2900 3244 3552 3833 4093 4337 4567 4785 4993 5193]
```

## 测试结果

```
--- PASS: TestSlowStartGrowth                 慢启动每 ACK +1 MSS、每 RTT 翻倍、封顶 ssthresh
--- PASS: TestCongestionAvoidanceAdditive     拥塞避免每 ACK +MSS*MSS/cwnd
--- PASS: TestTimeoutBackoffRules             cwnd=1 MSS、ssthresh 减半、RTO 指数退避封顶
--- PASS: TestDupAckDoesNotGrowWindow         重复/旧 ACK 不增 cwnd、不增有效窗口
--- PASS: TestCumulativeAckClampedToRealBytes ACK 跳跃钳制到 sndNxt
--- PASS: TestOldAckNeverNegativeWindow       旧 ACK 不使窗口为负
--- PASS: TestDupAckDoesNotTouchRetransmitTimer 重复 ACK 不重置重传计时器
--- PASS: TestFullTransferInOrder             无丢包完整传输
--- PASS: TestLossRecovery                    丢包后经超时回退恢复并完成
--- PASS: TestInvariantsUnderLoss             确定性丢包压测下不变式全程成立
PASS
ok  	tcpsim
```
