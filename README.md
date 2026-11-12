# WebSocket压缩上下文分片

WebSocket消息跨帧压缩时，控制帧不能进入解压字典。写供Go消息宿主调用的接收组件，按消息协商的上下文保留规则复用字典，只有首数据帧能声明压缩，续帧只延续本消息；输出和压缩步骤共享预算，非法尾部丢弃整消息。用注入压缩器测试控制帧穿插、空分片与消息间字典清理。

## 组件

- `Receiver.HandleFrame`：消息宿主逐帧调用。数据帧按 FIN 重组；控制帧（close/ping/pong）立即作为 `EventControl` 返回，不进入压缩缓冲与解压字典，因此可安全穿插在分片消息之间。
- 压缩协商：仅消息首数据帧允许置 RSV1；续帧或控制帧置位返回 `ErrRSV1` 并丢弃整条消息。压缩载荷在 FIN 时拼齐，追加 RFC 7692 的 `0x0000ffff` 同步块后一次性解压；若载荷本身仍带该尾部（发送方应剥离），返回 `ErrIllegalTrailer` 并丢弃整消息。
- 字典规则：`ContextTakeover=true` 时跨消息复用字典（`flateDecompressor` 自行保留最近 32KiB 历史，经 `NewReaderDict` 恢复，因为标准库 `flate.Reset` 会清空窗口）；`false` 时每条消息结束后 `Reset`。解压失败或非法尾部无条件 `Reset`，防止坏流污染后续消息。
- 共享预算：`Budget` 对“保留的压缩输入字节”和“解压输出字节”计费，任一处超限返回 `ErrBudget` 并丢弃整条消息；未压缩消息只按输入计费一次。

## 使用

```go
r := netsec.NewReceiver(netsec.ReceiverConfig{
    Decompressor:    netsec.NewFlateDecompressor(true), // 按协商结果传入 takeover
    ContextTakeover: true,
    Budget:          netsec.NewFixedBudget(1 << 20),
})
defer r.Close()

ev, err := r.HandleFrame(frame)
switch {
case err != nil:
    // 消息级错误：当前消息已丢弃，状态机已回到就绪态
case ev.Kind == netsec.EventControl:
    // ev.Frame 为穿插的控制帧
case ev.Kind == netsec.EventMessage:
    // ev.Message 为重组完成的数据消息
}
```

`Decompressor` 与 `Budget` 均为接口，测试通过注入假压缩器断言：控制帧不进字典、空分片/空消息、消息间字典清理、非法尾部丢弃与预算耗尽。
