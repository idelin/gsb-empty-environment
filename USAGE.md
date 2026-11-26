# RFC 9002 丢包检测与拥塞事件状态机

根包 `rfc9002`（module `prescreen.local/ps-8dea28f4a0`）仅依赖标准库，可直接导入。

```go
import rfc9002 "prescreen.local/ps-8dea28f4a0"

ld := rfc9002.NewServer(rfc9002.WithMaxAckDelay(25 * time.Millisecond))
ld.RecordBytesReceived(1200)
ld.SendAckEliciting(rfc9002.Initial, 0, now, 1200)
ack := ld.OnAckReceived(rfc9002.AckFrame{
    Space: rfc9002.Initial, LargestAcked: 0,
}, now.Add(50*time.Millisecond))
timer := ld.SetLossDetectionTimer(now) // 统一单一定时器
```

所有时间都以显式 `time.Time` 参数传入，便于用虚拟时钟做确定性测试。

## 关键 API

- 三空间：`rfc9002.Initial` / `Handshake` / `ApplicationData`，各自维护
  `Spaces[s].SentPackets`、`LargestAcked`、`LossTime`、`InFlightBytes`。
- 发送 / ACK：`OnPacketSent`、`SendAckEliciting`、`SendPureAck`、
  `OnAckReceived(AckFrame, now)`、`AckOne` / `AckNumbers` / `AckUpTo`。
- RTT：内嵌 `RTTEstimator`，暴露 `LatestRTT` / `MinRTT` / `SmoothedRTT` / `RTTVar`。
  仅当 ACK 的 `LargestAcked` 为本帧新确认、且新确认集合含 ack-eliciting 包时才采样；
  `max_ack_delay` 仅在 `ApplicationData && HandshakeConfirmed` 后扣减，
  有效延迟被 `min(max_ack_delay, latest_rtt-min_rtt)` 限幅。
- 双门限丢包：`DetectLostPackets`，包门限 `PacketThreshold = 3`，时间门限
  `LossDelay() = max(9/8*max(LatestRTT, SmoothedRTT), Granularity)`。
- 单一定时器：`GetLossTimeAndSpace`、`GetPtoTimeAndSpace`（同时提供同名包级函数）、
  `SetLossDetectionTimer` / `NextDeadline`、`OnLossDetectionTimeout`。
  loss deadline 优先，否则 PTO；反放大命中时撤防。
- 反放大：`NewServer`、`BytesSent` / `BytesReceived`、
  `AntiAmplificationLimited`、`ReceiveClientPacket`、`ValidateAddress`。
  命中 3 倍上限时统一定时器撤防，且 PTO 超时不自增 `PTOCount`（杜绝空转），
  收到新客户端包解除限制后重设。
- 密钥丢弃：`DiscardKeys(Initial | Handshake)` 清理该空间在途字节、已发表与
  loss timer，重置 `PTOCount`，再重算统一 timer。
- 拥塞：`CongestionWindow` / `SlowStartThreshold` / `RecoveryEpoch`、
  `CongestionEvent`、`OnECNCE` / `OnECNCECount`，每个 Recovery Epoch 至多折半一次；
  `OnPersistentCongestion` 把窗口重置到 `MinimumWindow`，之后 ACK 按慢启动恢复。
  RFC 7.6.2 的时间跨度持久拥塞检测在丢包路径默认开启，可用
  `Config.DisableAutomaticPersistentCongestion` 关闭。

## 验收与运行

`acceptance/acceptance_test.go` 以外部测试包覆盖全部验收点：

| 验收点 | 测试 |
| --- | --- |
| 跨三空间乱序 ACK / 包门限 | `TestCrossSpaceOutOfOrderAcks` |
| 时间门限与统一定时器 | `TestTimeThresholdAndUnifiedTimer` |
| 反放大锁死与解除 | `TestAntiAmplificationLockAndRelease` |
| 握手确认前后 `max_ack_delay` | `TestMaxAckDelayGatingBeforeAfterConfirm`、`TestRTTSampleEligibilityAndClamp` |
| 纯 ACK 不触发 PTO | `TestPureAckDoesNotTriggerPTO` |
| 空间密钥丢弃清理 | `TestKeyDiscardCleanup` |
| ECN-CE 每周期至多折半一次 | `TestECNCEOncePerRecoveryEpoch`、`TestLossHalvingOncePerEpochEndToEnd` |
| 持久化拥塞重置与恢复 | `TestPersistentCongestionResetAndRecovery`、`TestAutomaticPersistentCongestionDetection` |
| PTO 退避 / 空间差异 | `TestPTOBackoffAndMaxAckDelaySpace`、`TestLossDelayFormula` |

```sh
go test -count=1 ./...
go vet ./...
gofmt -l .
```
