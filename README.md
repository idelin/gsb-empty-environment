# Alt-Svc通告来源与证书

攻击者在明文响应里注入 Alt-Svc，或让通告指向自己控制的端点，把客户端后续请求引到恶意服务器。为Go HTTP 客户端写供连接选择调用的 Alt-Svc 缓存包：只接受来自 HTTPS 响应的通告；每条含协议标识（h2、h3）、可选主机、端口、ma（缺省 86400 秒，上限取 7 天）和 persist；通告值为 clear 时删除该源全部条目；使用替代端点前须验证其证书对原始源的主机名有效，不能按替代主机名验证；替代端点握手失败后该条目禁用 5 分钟；同一源最多保留 8 条，超出淘汰最先过期的。用明文通告、ma 超限、clear、证书只对替代主机有效和第 9 条通告验证。

## 用法

```go
cache := netsec.New()
cache.Store(resp) // 明文响应中的 Alt-Svc 会被忽略

origin := "https://origin.example"
entry, ok := cache.Select(origin)
if ok {
	// DialTLSContext 使用原始源主机名验证证书，而不是 entry.Host。
	conn, err := netsec.DialTLSContext(ctx, "tcp", entry.Address(), origin, &tls.Config{
		RootCAs: roots,
	})
	if err != nil {
		cache.MarkFailure(origin, entry) // 该条目禁用 5 分钟
	}
}
```

HTTP/3 等非 TCP 传输可调用 `TLSConfigForOrigin`，并在 QUIC/TLS 配置中使用返回的
`ServerName`。`Lookup` 返回未过期条目（包含处于禁用冷却期的条目），`Candidates`
和 `Select` 只返回当前可用的条目。

## 安全语义

- 仅当 `http.Response.TLS != nil` 且请求为 HTTPS 时接受通告。
- 只接受 `h2`、`h3`；`ma` 缺省为 86400 秒，超过 604800 秒按 7 天处理。
- 通告主机可省略（使用原始源主机），端口必须有效。
- `clear` 删除该原始源的全部缓存条目。
- TLS 验证名始终是原始源主机名，绝不使用替代端点主机名。
- 握手失败后条目保留但禁用 5 分钟，重新通告不会提前解除禁用。
- 每个原始源最多 8 条，超出时淘汰最早过期的条目。

运行验证：

```sh
go test -race ./...
```
