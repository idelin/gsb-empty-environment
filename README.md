# SASL通道绑定类型校验

认证服务支持不同通道绑定方法时，空绑定不能被当作某种绑定成功。写供Go认证宿主调用的协商包，客户端声明、服务端要求与传输提供的绑定类型三方一致才接受，绑定数据包括服务身份和握手实例；不支持与验证失败使用不同内部结果但都不能回退无绑定。用替身TLS导出器测试类型互换、空值及跨连接复用。

## `channelbinding` 包

`channelbinding` 是供认证宿主调用的通道绑定协商包，支持 `tls-exporter`
（RFC 9266）与 `tls-server-end-point`（RFC 5929）两种绑定类型。

- `Policy.Verify`（或包级 `Negotiate`）要求客户端 `Declaration`、服务端
  `Policy` 与 `Transport` 三方声明同一绑定类型，且 `Evidence` 能由传输的
  `Exporter` 重新导出、常量时间比对一致才返回 `StatusBound`。
- `Evidence` 同时绑定服务身份（导出器 context 与 `ServiceID` 字段）和握手
  实例（`HandshakeKey`），跨连接复用返回 `ErrHandshakeMismatch`。
- 空类型、空 material、空握手键一律返回 `ErrEmptyBinding`，永远不会被当作
  某种绑定成功；无绑定声明夹带证据也按非法拒绝。
- `StatusUnsupported`（类型协商失败）与 `StatusInvalid`（类型一致但数据验证
  失败）是不同内部结果；除策略显式 `AllowNone` 的无绑定成功外，任何失败都带
  非空错误且绝不返回 `StatusUnbound`。
- `NewTLSTransport` 将 `*tls.Conn` 适配为 `Transport`；测试用实现
  `Exporter` 接口的替身导出器，覆盖类型互换、空值与跨连接复用。

```go
tr, _ := channelbinding.NewTLSTransport(tlsConn, channelbinding.TLSExporter)
ev, err := channelbinding.Bind(tr, "imap/host.example.org")
status, err := policy.Verify(channelbinding.Declaration{Type: channelbinding.TLSExporter}, ev, tr)
```

运行测试：`go test ./...`
