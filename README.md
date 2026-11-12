# DoH与DoT严格解析链

网络攻击者可阻断853和443端口，诱使解析器退回明文DNS，或在引导阶段把DoH主机名指向被污染的地址。给Go本机存根解析器写供查询调度调用的上游选择包：严格策略下，DoH与DoT上游的认证域名和SPKI固定值由调用方给出，握手身份不符、DoH内容类型不是application/dns-message、应答问题段与请求不一致都算失败；严格模式失败不得退回明文，机会模式退回必须记录降级。引导地址只作连接提示，不改变证书校验名。用端口阻断、错误证书、问题段改写和合法回退验证。

## 调用方式

```go
pins := []string{"base64-sha256-spki-pin"}

selector, err := netsec.NewSelector(netsec.Strict, []netsec.Upstream{
	// URL 主机名可以解析到任意地址；Bootstrap 只影响拨号目标。
	netsec.DoHUpstream(
		"doh-primary",
		"https://resolver.example/dns-query",
		"resolver.example",
		pins,
		"192.0.2.10:443",
	),
	netsec.DoTUpstream(
		"dot-primary",
		"resolver.example",
		pins,
		"192.0.2.11:853",
	),
	// 严格模式会跳过这项；机会模式才会在安全上游全部失败后使用。
	netsec.PlainUpstream("plain-fallback", "192.0.2.12:53"),
}, netsec.WithLogger(logger))
if err != nil {
	// 配置错误，例如严格模式缺少 AuthName 或 SPKI pin。
}

result, err := selector.ResolveQuestion(ctx, netsec.NewQuestion("example.test", netsec.TypeA))
if err != nil {
	// Strict: errors.Is(err, netsec.ErrStrictNoFallback) 为真，且未发送明文查询。
	// Opportunistic: 所有上游都失败。
}
if result.Degraded {
	// Opportunistic 回退成功；同时写入 logger，并可通过 Degradation 审计。
	_ = result.Degradation
}
```

也可以直接把调度器已有的 DNS 报文交给 `Exchange` / `Resolve`。`SPKIPinForCert` 可
从 `*x509.Certificate` 生成标准 base64 SPKI 固定值。

## 安全边界

- `Strict` 只调用 DoH 和 DoT。安全上游失败时返回错误，不会读取
  `PlainUpstream`，因此端口阻断不能诱发明文降级。
- `Strict` 要求每个安全上游显式提供 `AuthName` 和至少一个 `SPKIPins`；
  `Opportunistic` 也接受并强制校验这两个字段，但允许省略 pin。
- `Bootstrap` / `Address` 只传给拨号器。TLS 的 `ServerName` 始终来自
  `AuthName`，不会因为引导地址是 IP 或污染域名而改变。
- TLS 默认执行系统 PKI 校验，并可用 `RootCAs` 注入测试或私有根；存在 pin 时，
  `VerifyConnection` 还在校验后的证书链上做 SHA-256 SPKI 固定值比对。
- DoH 只接受 HTTPS、POST、2xx 和 `application/dns-message`；重定向被禁用。
  每种传输都会校验事务 ID、问题数量和每个问题的名称/类型/类别。
- `Opportunistic` 的每次回退都会产生 `Degradation`（含时间、来源、目标和失败
  原因）、调用 `slog` 的 Warn 日志，并可通过 `WithDegradationHook` 接入审计。

## 验证

`selector_test.go` 使用内存拨号器与真实 TLS/HTTP 握手覆盖：

- 853/443 虚拟端口拒绝连接时，严格模式零明文查询；
- 证书域名不符、SPKI pin 不符时握手失败；
- DoH `Content-Type` 不是 `application/dns-message` 时失败；
- 应答问题段被改写时失败；
- 引导地址指向内存服务器、证书校验名保持独立；
- 机会模式在安全上游失败后合法回退，并同时记录日志和降级元数据。

运行：

```sh
go test ./...
```
