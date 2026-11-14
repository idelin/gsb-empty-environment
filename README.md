# netsec

可导入的 Go HTTP 追踪上下文组件。它把 W3C `traceparent` / `tracestate`
与租户、来源、时间戳一起做 HMAC-SHA256 签名；只有格式、长度、签名、时效和
本地租户/来源绑定全部通过的入站上下文，才允许继承 trace ID。每个请求都会
生成新的本地 span，采样标志只由本地策略决定。

## 快速使用

```go
key := []byte("replace-with-at-least-32-random-bytes")

tracer, err := netsec.New(netsec.Config{
    SigningKey: key,
    TenantID:   "tenant-a",
    Origin:     "edge-a",
    Sampler:    netsec.MustRateSampler(0.10),
})
if err != nil {
    log.Fatal(err)
}

mux.Handle("/", tracer.Middleware(handler))
```

处理器通过上下文读取可信结果：

```go
tc, ok := netsec.FromContext(r.Context())
if ok {
    log.Printf("trace=%s local_span=%s parent=%s sampled=%t trusted=%t",
        tc.TraceID, tc.SpanID, tc.ParentSpanID, tc.Sampled, tc.Trusted)
}
if err := netsec.RejectionFromContext(r.Context()); err != nil {
    // 入站追踪头被拒绝；请求已隔离到新的本地 trace。
}
```

出站调用显式签名：

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
if err := tracer.Inject(ctx, req.Header); err != nil {
    return err
}
resp, err := http.DefaultClient.Do(req)
```

多租户场景用 `Bind` 从已认证请求身份解析租户和来源。`Bind` 是唯一权威来源，
不会读取追踪头中的租户或来源。追踪中间件必须位于认证中间件内侧，或由
`Bind` 从已经写入请求上下文的认证结果读取身份：

```go
Bind: func(r *http.Request) (netsec.Binding, bool) {
    principal, ok := authn.PrincipalFrom(r.Context())
    if !ok {
        return netsec.Binding{}, false
    }
    return netsec.Binding{
        TenantID: principal.TenantID,
        Origin:   principal.ServiceName,
    }, true
}
```

## 线上格式

| Header | 说明 |
| --- | --- |
| `traceparent` | 严格 W3C version 00，正好 55 字节，小写十六进制，拒绝全零 ID |
| `tracestate` | 可选，最多 512 字节、32 个成员，拒绝重复/非法 key |
| `X-Trace-Tenant` | 已签名租户标识，必须与本地认证绑定一致 |
| `X-Trace-Origin` | 已签名来源标识，必须与本地认证绑定一致 |
| `X-Trace-Timestamp` | 已签名 Unix 秒，限制过期时间与未来时钟偏移 |
| `X-Trace-Signature` | `v1.<key-id>.<base64url-hmac-sha256>` |

单头及聚合长度受限；同名头重复出现时直接拒绝，不做合并。签名使用长度前缀
编码，覆盖 traceparent、原始 tracestate、租户、来源、时间戳和 key ID。
比较使用常量时间 HMAC 校验。

## 安全语义

- 未签名、签名错误、过期、超长、格式错误或跨租户/来源的入站上下文不会贡献
  trace ID、parent span 或 sampling flag；请求改为全新本地 trace。
- 合法入站上下文可以继承 trace ID，但入站 span 仅作为 `ParentSpanID` 链接
  元数据，本地 `SpanID` 总是重新生成；出站 `traceparent` 使用本地 span。
- `TraceContext.Sampled` 只由本地 `Sampler` 计算。入站 flags 仅保存在
  `RemoteSampled` 供审计，不能强制采样或逃避采样。默认策略为始终采样。
- 中间件在进入业务处理器前重建全部追踪头，因此盲目透传 `r.Header` 也不会
  转发攻击者提供的伪造值；跨服务建议仍显式调用 `Inject`。
- 信任域内服务共享签名密钥。跨到不同密钥的信任域时，入站上下文按不可信处理
  并开启新 trace，这是预期行为。密钥应通过密钥管理系统注入并定期轮换。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试覆盖伪造 trace ID/父子跨度/采样标志/租户/来源/签名、跨边界合法透传、
本地采样覆盖远端 flag、合法追踪以及同一入站上下文的边界 span 重生成。
