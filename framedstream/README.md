# framedstream

带长度前缀的双向流状态机库（sans-IO）。调用方通过 `Input` 喂入字节、
用 `Outbound` 取走待发字节，状态机以 `Event` 形式向上层汇报。

## 线格式

```
frame   := uint32be length | payload[length]   (length <= MaxPayload)
control := uint32be 0xFFFFFFFF | byte type
type    := 0x01 FIN（半关闭） | 0x02 RST（重置）
```

## 状态机

每个方向独立：

- 发送：`open → closed（CloseSend 发出 FIN）`，任何状态可被 `Reset` 终止。
- 接收：`open → closed（收到 FIN）`，收到 RST / 协议错误 / 本地 Reset 时终止。

半关闭只关闭本端发送方向：发出 FIN 后 `SendFrame` 返回 `ErrSendClosed`，
但 `Input` 继续交付对端剩余的帧，直到对端也 FIN 或会话被重置。
`Reset` 立即终止两个方向，丢弃重组缓冲，并向对端排队一个 RST。

## 正常结束 vs 对方未发完

传输层关闭时调用 `TransportClosed`，按接收状态区分三种结局：

- **正常结束**：已收到对端 FIN（`EventPeerFinished`），`TransportClosed`
  不再产生事件。对端明确宣告"不会再发"，且最后一帧完整。
- **对方未发完（aborted）**：未收到 FIN，但也没有半帧残留，产生
  `EventPeerAborted`。已交付的帧都完整，但对端从未宣告结束——
  它可能还有没来得及发的帧。
- **截断（truncated）**：关闭时缓冲区里有半帧（半个长度头或半个
  payload），产生 `EventPeerTruncated`，`Partial` 字段给出残留字节数。
  最后一帧不完整，必须视为数据损坏。

简言之：FIN = 对端说"我说完了"；aborted = 对端没说完整句话就挂断；
truncated = 对端说到一半被掐断。

## 测试

`TestByteSplitting` 对同一段字节流枚举所有单切分点、所有双切分点
（三段）以及逐字节喂入，要求产出的事件序列与一次性喂入完全一致，
从而覆盖任意读边界。其余测试覆盖半关闭双向独立、重置、截断、
空帧与超大帧。
