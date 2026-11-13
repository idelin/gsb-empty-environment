# 设置帧上限一次

SETTINGS里的MAX_HEADER_LIST_SIZE在头块已经开始后变小，已经接收的巨大头仍被接受。新上限只对之后开始的头块生效；单个头块超过当前上限则该流失败。上限之内的头块成功。可导入的Go状态机接协议网关送来的状态。先发送一半头，再把上限降到已发送字节数以下。

## 实现

`package netsec` 提供连接级、传输无关的解码侧状态机 `Connection`，网关可以
用类型化方法或 `HandleEvent` 事件接口喂入观察到的帧。

关键语义：

- 头块在 `HEADERS` 开始时快照当前 `MAX_HEADER_LIST_SIZE`，整个块（含后续
  `CONTINUATION`）都按该快照判定；设置中途变小不影响在途块。
- `SetMaxHeaderListSize` 只约束调用之后才开始的头块；`0` 表示不限制。
- 单块累计字节超过其快照上限时，该流以 `ENHANCE_YOUR_CALM` 失败，错误可用
  `errors.Is(err, ErrHeaderListTooLarge)` 匹配，并包装为 `*StreamError`。
- 上限之内的头块正常完成；`CONTINUATION` 无活动块、活动块上再发 `HEADERS`
  等帧错误返回 `ErrProtocol`。

典型用法（先发一半头，再把上限降到已发送字节数以下）：

```go
conn := netsec.NewConnection(100)
conn.HeadersFrame(1, 50, false)       // 头块开始，快照上限 100
conn.SetMaxHeaderListSize(20)         // 调小到已发送字节以下
err := conn.ContinuationFrame(1, 50, true) // 仍按快照 100 判定，成功

err = conn.HeadersFrame(3, 21, true)  // 新块按 20 判定 -> 流失败
// errors.Is(err, netsec.ErrHeaderListTooLarge) == true
```

网关事件入口：`SettingUpdateEvent`、`HeadersEvent`、`ContinuationEvent`、
`StreamResetEvent`，统一经 `Connection.HandleEvent` 投递。

验证：`go test ./...`。
