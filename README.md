# WebTransport 会话流绑定

攻击者在同一条HTTP/3连接上拿别的会话ID开WebTransport流，想写进他人会话。给Go服务端写供流分发器调用的会话注册包：流与数据报按CONNECT流编号绑定会话；未建立会话的流缓冲不超过调用方上限并超时重置，已关闭会话的流直接重置；Origin不在白名单的CONNECT拒绝。用伪造ID、关闭后到达和缓冲溢出验证。

## 用法

每个 HTTP/3 连接创建一个 `Registry`。流分发器把 CONNECT 请求的 stream ID 和 `Origin` 交给 `RegisterConnect`，把后续流的载荷及重置函数封装为 `Stream`，把数据报载荷交给 `HandleDatagram`。

```go
registry := netsec.New(netsec.Options{
    AllowedOrigins:     []string{"https://app.example"},
    PendingBufferLimit: 64 << 10,
    PendingTimeout:     2 * time.Second,
})

session, err := registry.RegisterConnect(netsec.ConnectRequest{
    StreamID: netsec.ConnectID(connectStreamID),
    Origin:   request.Header.Get("Origin"),
}, sink)
if err != nil {
    // Send an error response for the CONNECT; no session is created.
}

outcome := registry.HandleStream(netsec.ConnectID(connectStreamID), stream)
if outcome == netsec.OutcomeReset {
    // stream.Reset was already called by the registry.
}

registry.HandleDatagram(netsec.ConnectID(connectStreamID), datagram)
session.Close() // Subsequent traffic for this CONNECT ID is reset/dropped.
```

`Sink` receives accepted traffic:

```go
type Sink interface {
    DeliverStream(*Session, StreamData)
    DeliverDatagram(*Session, []byte)
}
```

## 安全语义

- CONNECT 的 `Origin` 必须与 `AllowedOrigins` 精确匹配；空白名单默认拒绝，`null`、缺失、带路径、userinfo 或伪造成相似域名的 Origin 都会被拒绝。
- 会话以 CONNECT 请求流编号为键。伪造的、尚未注册的会话 ID 不会路由到任何现有会话。
- 尚未注册的流按 CONNECT ID 分开缓冲，每个 ID 不超过 `PendingBufferLimit` 字节；超限立即 `Reset`，超时后缓冲流全部 `Reset`。
- 关闭会话会留下 ID 墓碑；关闭后到达的流立即 `Reset`，数据报直接丢弃，且该 ID 不能再次注册。
- 数据报不缓冲；未注册或已关闭会话的数据报直接丢弃。
- `MaxPendingConnects` 和 `MaxPendingStreams` 限制伪造 ID 造成的元数据内存占用。

`Registry` 必须在单个 HTTP/3 连接内使用，不能跨连接共享。CONNECT stream ID 在同一连接内唯一，但注册包只负责按 ID 路由；跨用户授权仍应由 CONNECT 的 Origin 白名单和上层身份校验完成。

`ConnectRequest.StreamID` 和 `HandleStream` 的 `connectID` 必须来自 HTTP/3 栈对请求流/会话关联的解析结果，不能直接采用客户端在头、body 或 capsule 中自报的值；否则攻击者可以要求以他人的 CONNECT ID 注册或分发。

## 验证

```sh
go test ./...
go test -race ./...
```
