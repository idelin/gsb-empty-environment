# 隧道建立前数据隔离

CONNECT请求尚未获得目标授权时，客户端可能提前发送隧道数据。为Go代理实现隧道接入组件，授权、拨号、成功响应与数据转发按明确屏障执行，预读数据有上限且仅在成功响应完成后交付；目标或策略改变须重新授权。用可暂停回调测试早到数据、取消和响应发送失败，确认未授权目标收到零字节。

## 使用

`Ingress.Serve` 接收已经解析完 CONNECT 请求头的客户端连接，并把其后的第一个字节视为隧道流：

```go
ingress := netsec.NewIngress(
	netsec.WithAuthorizeFunc(func(ctx context.Context, req netsec.Request) error {
		return policy.Allow(ctx, req.Target.Address, req.Policy)
	}),
	netsec.WithMaxEarlyData(8<<10),
)

err := ingress.Serve(ctx, clientConn, netsec.Request{
	Target: netsec.NewTarget("example.com:443"),
	Policy: netsec.Policy{ID: "proxy-default"},
})
```

接入顺序固定为：授权成功 → 配置版本仍有效 → 拨号 → 版本仍有效 → 成功响应写完 → 版本仍有效 → 交付预读数据并开始双向转发。任何一步失败或取消都会关闭连接并丢弃预读数据。

`ConfigStore` 用于运行中的目标/策略切换。其 `Revision` 在每个屏障被复核；变化后旧授权立即失效，隧道会在拨号或响应前重新授权。成功响应已经发出后发生配置变化时，接入器选择中止而不是向未重新授权的目标转发数据。

`Handler` 提供 `net/http` 适配，会保留 HTTP 服务器在 CONNECT 请求头之后已经缓冲的早到字节。
