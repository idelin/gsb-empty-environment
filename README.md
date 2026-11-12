# 隧道建立前数据隔离

CONNECT请求尚未获得目标授权时，客户端可能提前发送隧道数据。为Go代理实现隧道接入组件，授权、拨号、成功响应与数据转发按明确屏障执行，预读数据有上限且仅在成功响应完成后交付；目标或策略改变须重新授权。用可暂停回调测试早到数据、取消和响应发送失败，确认未授权目标收到零字节。

## 使用

`Gateway.Serve` 按 `authorize -> dial -> success response -> forward` 的屏障建立隧道：

```go
gateway := &netsec.Gateway{
	Policy: func() string { return policyRevision.Load() },
	Authorize: func(ctx context.Context, req netsec.Request) (netsec.Decision, error) {
		return policy.Authorize(ctx, req.Target, req.PolicyVersion)
	},
	Dial: func(ctx context.Context, req netsec.Request) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", req.Target)
	},
	Respond: func(ctx context.Context, w io.Writer) error {
		return netsec.WriteConnectionEstablished(w)
	},
	MaxEarlyData: 64 << 10,
}
```

调用方从 CONNECT 中解析 `Request.Target` 并把自己持有的策略版本放入
`Request.PolicyVersion`（或由 `Gateway.Policy` 提供）。`Serve` 不会跨请求缓存授权：
目标或策略版本变化都会重新调用 `Authorize`。授权通过前不会拨号；成功响应
完整写出并 flush 之前，早到的客户端数据只保存在有上限的缓冲区中，目标连接
不会收到任何字节。
