# HTTP 数据报流号解复用隔离

HTTP/3 的 HTTP Datagram 靠 Quarter Stream ID 找到所属请求流，攻击者可以发送指向别人的流、已关闭的流或未协商能力的数据报。本模块为 Go 的 HTTP/3 服务端提供连接级的数据报校验与分发（RFC 9297），不依赖第三方库。

处理规则：

- 双方未都设置 `SETTINGS_H3_DATAGRAM` 时，收到数据报返回 `ErrH3DatagramError`，服务端应以 `H3_DATAGRAM_ERROR`（`0x33`）关闭连接。
- Quarter Stream ID 乘 4 后必须是该连接上对端发起的双向流；否则静默丢弃。
- 流必须已由 CONNECT 类请求建立数据报通道，否则静默丢弃。
- 流尚未创建时，每个流最多缓冲 16 个数据报，满后丢弃最旧的。
- Quarter Stream ID 超过 `2^60 - 1` 时返回 `ErrH3DatagramError`。
- 已关闭的流保留墓碑，之后到达的数据报静默丢弃，不能通过缓冲重新交付。

## 使用

每个 HTTP/3 连接创建一个 `Dispatcher`。服务端把流生命周期事件和收到的 QUIC DATAGRAM 负载交给它：

```go
d := netsec.NewDispatcher(
	netsec.WithSettings(localH3Datagram, peerH3Datagram),
	netsec.WithHandler(func(streamID uint64, payload []byte) {
		// payload 已去掉 Quarter Stream ID 字段。
	}),
	netsec.WithConnectionErrorHandler(func(code uint64, reason string) {
		// 使用 code 终止 HTTP/3 连接。
	}),
)

// 请求头解析完成时：
if method == "CONNECT" {
	d.OpenConnectStream(streamID)
} else {
	d.OpenStream(streamID)
}

// 每条 QUIC DATAGRAM 的负载：
if err := d.HandleDatagram(framePayload); err != nil {
	// err 为 ErrH3DatagramError。
}

// 请求流关闭时：
d.CloseStream(streamID)
```

`HandleQuarterStreamID` 接收已解出的 Quarter Stream ID；`HandleStreamDatagram` 接收完整流 ID，并会对非对端双向流做静默丢弃。`EncodeQuarterStreamID` / `EncodeVarint` 可用于构造和测试线格式。

流号是连接内局部标识，所以 `Dispatcher` 绝不能跨连接复用；否则不同连接上同号的流会被混淆。

## 测试

```sh
go test -race ./...
```

测试覆盖未协商、非本连接流号、已关闭流、缓冲上限与 FIFO 淘汰、CONNECT 建立前缓冲及超限 Quarter Stream ID。
