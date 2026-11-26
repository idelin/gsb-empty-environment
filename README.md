QUIC 发送端在三个包号空间（Initial、Handshake、ApplicationData）上并行推进丢包恢复与探测超时，错误的确认延迟扣减或跨空间 PTO 选择会引发无谓重传或死锁。为传输协议栈交付可导入的 Go 恢复状态机包，按 RFC 9002 精确建模。输入按微秒递增的发送、收到 ACK（含最大确认号、确认延迟、多段确认区间）、路径验证切换和时钟推进事件，以及对端 max_ack_delay。规则包含：按包阈值 3 或时间阈值 max(9/8*max(srtt,latest_rtt), 1ms) 判定丢包；仅在应用空间且最新最大确认包为需确认包、且扣减后不小于 min_rtt 时才减去经 max_ack_delay 截断的 ack_delay 更新 srtt 与 rttvar；计算全空间最早的丢包定时器或 PTO 绝对时刻（握手未确认时忽略应用空间 PTO，且在途无需确认包时仅按反放大或握手状态触发）。组件对每事件返回新判丢包号、实时在途字节数、最新 RTT 三元组与下一次定时器时刻。验收按 RFC 9002 附录伪代码构造事件序列逐微秒核对，覆盖纯 ACK 包不计入在途、乱序迟到 ACK 不回退最大确认号、ack_delay 超过 latest_rtt-min_rtt 时的保护截断、以及握手确认前后 PTO 空间切换。

## 实现说明

根目录就是可导入包 `recovery`；`recovery/` 子包仅转发同一套 API，适合约定使用 `<module>/recovery` 导入路径的工程。所有时间、RTT、`ack_delay`、`max_ack_delay` 和定时器绝对值均为 `int64` 微秒；无定时器时 `Result.NextTimeout()` 与 `NextTimeoutAt` 返回 `-1`。

```go
import recovery "prescreen.local/ps-4b0c066dfd"

rm := recovery.New(25_000) // 对端 max_ack_delay，单位微秒

send := rm.Send(recovery.SendEvent{
    Time:         0,
    Space:        recovery.ApplicationData,
    PacketNumber: 0,
    Bytes:        1200,
    AckEliciting: true,
})
_ = send.BytesInFlight
_ = send.NextTimeout()

ack := rm.Ack(recovery.AckEvent{
    Time:         100_000,
    Space:        recovery.ApplicationData,
    LargestAcked: 0,
    AckDelay:     2_000,
})
_, _, _ = ack.RTTTriple() // latest_rtt, smoothed_rtt, rttvar

tick := rm.AdvanceClock(ack.NextTimeout())
_ = tick.LostPacketNumbers
```

也可以使用统一事件入口：

```go
result := rm.Handle(recovery.Event{
    Kind: recovery.EventACK,
    ACK: &recovery.AckEvent{
        Time: 100_000, Space: recovery.ApplicationData,
        LargestAcked: 4,
        Ranges: []recovery.AckRange{recovery.Range(4, 4), recovery.Range(1, 2)},
    },
})
if result.Err != nil { /* invalid event */ }
```

## 关键语义

- 丢包阈值为 `largest_acked >= packet_number + 3`；时间阈值为 `max(9 * max(smoothed_rtt, latest_rtt) / 8, 1000us)`。
- 纯 ACK 可设置 `PureACK: true` 或 `AckEliciting: false`；默认不计入 `BytesInFlight`，也不会形成需确认 PTO 在途计数。
- 较小 `largest_acked` 的迟到 ACK 仍可通过多段区间确认更低包号，但不会回退 `LargestAcked`，也不会产生新的 RTT 样本。
- RTT 只在 `ApplicationData` 空间、且本次推进最大确认号所对应的包为 ack-eliciting 时更新。`ack_delay` 先截断到 `max_ack_delay`；若 `latest_rtt - ack_delay < min_rtt`，本次不应用扣减，但仍用未扣减样本更新 SRTT/RTTVAR，与 RFC 9002 伪代码一致。
- PTO 基础值为 `smoothed_rtt + max(4*rttvar, 1000us)`，ApplicationData 再加 `max_ack_delay`，并按 `2^pto_count` 指数退避。Initial/Handshake 不加 `max_ack_delay`。
- 握手未确认时跳过 ApplicationData PTO；所有候选空间中取最早绝对时刻。反放大限制生效时不设置 PTO；无 ack-eliciting 在途包且路径/握手验证未完成时保留握手死锁恢复 PTO。
- `AdvanceClock(t)` 会在 `t` 到达定时器时刻时处理时间阈值丢包或 PTO 退避；未到期的微秒级推进保持原绝对截止时间不变。

内置验收测试可直接运行：

```sh
go test ./... -v
```
