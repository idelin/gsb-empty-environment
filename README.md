# HTTP头部信任链

边缘代理需要从多跳转发信息中确定客户端地址，外部请求可自行伪造同名头。为Go网关实现请求来源组件，输入真实对端和从近到远的代理记录；只在当前跳属于配置的可信CIDR时消费下一跳，任一语法歧义拒绝，IPv4映射地址统一分类。保留消费路径并绑定配置版本。用实际HTTP测试直连伪头、多行冲突、IPv6端口及可信链中混入不可信节点。

## 用法

```go
resolver, err := netsec.NewResolver(netsec.Config{
    Version:      "edge-cfg-2026-11",
    TrustedCIDRs: []string{"10.0.0.0/8", "2001:db8::/32"},
})
if err != nil { /* 配置中的 CIDR 非法 */ }

// 作为 HTTP 中间件：解析结果存入请求上下文，歧义输入返回 400。
handler := resolver.Middleware(app)

// 在业务处理器中读取：
origin, ok := netsec.FromContext(r.Context())
// origin.ClientIP      最远可信跳地址
// origin.Path          已消费跳路径（近→远，首跳为真实对端）
// origin.ConfigVersion 产生该结果的配置版本
```

行为要点：

- 仅当当前跳命中可信 CIDR 时才消费下一条 `X-Forwarded-For` 记录，遇首个不可信跳即停止，后续记录不解析、不消费。
- 被消费的记录必须是纯 IP 字面量：端口、方括号、zone、前导零、空白、主机名等一律以 `ErrInvalidRecord` 拒绝。
- 多行同名头必须完全一致，否则以 `ErrConflictingHeaders` 拒绝。
- IPv4 映射地址（如 `::ffff:10.0.0.1`）统一按 IPv4 分类匹配 CIDR。

测试：`go test ./...`
