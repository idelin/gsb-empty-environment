# tcpsim — 离散事件 TCP 拥塞控制模拟器

纯 Go 标准库实现。所有包、队列和链路都运行在整数 tick 的离散事件引擎上，
不发任何真实网络流量。

## 模型

- **事件引擎**（`sim.go`）：最小堆调度，整数 tick；同 tick 事件按调度顺序 FIFO。
- **链路**（`link.go`）：FIFO 存储转发，序列化时延 `ceil(线长/速率)` + 固定传播
  时延，逐包排队；可注入确定性丢包。
- **发送方**（`tcp.go`）：
  - 慢启动：每个新 ACK `cwnd += min(新确认字节, MSS)`，直到 `cwnd >= ssthresh`。
  - 拥塞避免：每个新 ACK `cwnd += MSS*MSS/cwnd`（加性增）。
  - 超时回退：`ssthresh = max(cwnd/2, 2*MSS)`，`cwnd = 1 MSS`，`rto *= 2`
    （指数回退），重传最老未确认段；恢复完成后 RTO 降回基值。
  - 快速重传：3 个重复 ACK 触发，窗口减半；**不做**经典快恢复的窗口膨胀。
- **ACK 去重与重传计时分离**：
  - 去重路径（`onDupAck`）只统计/触发快速重传，绝不修改 `cwnd`、
    `sndUna`，也绝不触碰重传计时器 —— 重复 ACK 不会增加有效窗口。
  - 计时器（`armTimer`/`onTimeout`）是事件引擎上独立的代际（generation）
    保护的定时事件；ACK 路径仅重启或取消它。
- **累计 ACK 跳跃只确认真实未确认字节**：ACK 号先被钳制到 `sndNxt`，
  仅 `ack - sndUna > 0` 的部分计入窗口增长。
- **旧 ACK 不使窗口为负**：`ack <= sndUna` 直接去重路径；可用窗口
  `Usable() = max(0, cwnd - (sndNxt - sndUna))` 永远非负。
- **接收方**：带重组缓存的累计 ACK；重传填补空洞后 ACK 号可跳跃式前进。

## 运行

```sh
go test ./... -v     # 测试
go run ./cmd/demo    # 样例：60KB 传输，seq=10000 丢 1 次，seq=25000 丢 2 次
```

## 运行样例（`go run ./cmd/demo`，确定性输出经过裁剪）

```
[    85] slow-start ack=1000 cwnd=2000 ssthresh=8000
[   267] slow-start ack=7000 cwnd=8000 ssthresh=8000
[   267] phase -> congestion-avoidance (cwnd=8000 ssthresh=8000)
[   340] congestion-avoidance ack=8000 cwnd=8125 ssthresh=8000
[   259] DROP  data seq=10000 len=1000
[   364] fast retransmit seq=10000 ssthresh=4184 cwnd=4184
[   449] cumulative ACK jump: ack=18000 newly-acked=8000 bytes
[   542] DROP  data seq=25000 len=1000
[   708] fast retransmit seq=25000 ssthresh=2907 cwnd=2907
[   708] DROP  data seq=25000 len=1000
[  1027] TIMEOUT seq=25000 rto=400->800 cwnd=1000 ssthresh=2000
[  1112] cumulative ACK jump: ack=30000 newly-acked=5000 bytes
[  1112] recovery complete: rto restored to 400
...
--- summary ---
completed at tick      2131
bytes received in order 60000 / 60000
sender done             true
timeouts                1
fast retransmits        2
total retransmits       3
duplicate ACKs seen     11
packets dropped (fwd)   3
final cwnd / ssthresh   8078 / 2000
final rto               400 (base 400)
```

## 测试结果（`go test ./...`）

```
--- PASS: TestEngineOrdering                   事件按 tick 顺序、同 tick FIFO
--- PASS: TestLinkSerializationAndDelay        序列化 + 传播时延、排队
--- PASS: TestSlowStartThenCongestionAvoidance 慢启动指数增、拥塞避免加性增
--- PASS: TestTimeoutBackoffRules              超时 cwnd=1MSS、ssthresh 减半、rto 翻倍
--- PASS: TestDupAckNeverGrowsWindow           重复 ACK 不增加有效窗口
--- PASS: TestStaleAckKeepsWindowNonNegative  旧 ACK 不改状态、窗口不为负
--- PASS: TestCumulativeAckClampedToRealBytes  累计 ACK 跳跃只确认真实字节
--- PASS: TestReceiverBufferingAndCumulativeJump 重组缓存与累计 ACK 跳跃
--- PASS: TestFullTransferWithLoss             丢包端到端传输、RTO 恢复基值
PASS
ok      tcpsim
```
