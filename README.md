# SNI与连接合并身份绑定

同一HTTP/2连接上，攻击者握手时使用A站的SNI和证书，随后发送B站的:authority，想借已建立的信任访问B站后端或需要客户端证书的虚拟主机。给Go反向代理写供请求入口调用的连接身份绑定中间件：请求主机须被本连接已验证叶子证书的SAN覆盖（通配符只匹配一层，按小写Punycode并去掉末尾点比较），且调用方策略允许两个主机合并，否则返回421；要求客户端证书的虚拟主机必须使用它自己的握手。用大小写、尾点、通配符层级、IDN和客户端证书跨主机验证。

## 实现

零第三方依赖，仅使用标准库。

- `binder.go`：核心中间件 `Binder`，在请求入口对每个请求做连接身份绑定，违规统一返回 `421 Misdirected Request`。
- `san.go`：叶子证书 SAN 匹配，只认 SAN（不回退 CommonName），DNS 名按小写 Punycode、去尾点比较，通配符 `*` 仅允许最左完整标签且只匹配一层，IP 走 `net.IP` 比较。
- `idna.go`：自包含的 RFC 3492 Punycode 与聚焦 IDNA `ToASCII`，负责小写化、尾点去除、U-label/A-label 归一、IP 规范化。
- `tracker.go`：`SNICertTracker`，在 `GetCertificate`/`GetConfigForClient` 中记录每次握手选中的叶子证书，并按连接 SNI 取回（标准库不在 `tls.ConnectionState` 暴露服务端叶子证书）。

## 绑定规则（按顺序）

1. 请求 `:authority`/Host 必须可规范化（剥离端口、括号、尾点）。
2. 连接必须是 TLS 且携带非空 SNI；SNI 与请求主机都规范化为小写 Punycode。
3. 必须能解析到本连接握手选中的已验证叶子证书，否则失败关闭。
4. 跨主机时，要求客户端证书的虚拟主机、或本连接已带客户端证书，一律拒绝（必须独立握手）。
5. 跨主机还须由调用方 `CoalescePolicy` 显式允许合并；无策略时只允许同主机。
6. 请求主机必须被叶子证书 SAN 覆盖，否则 421。

## 接入示例

```go
tracker := netsec.NewSNICertTracker()
_ = tracker.Register(certA, "a.example")
_ = tracker.Register(certB, "b.example")

tlsConfig.GetCertificate = tracker.GetCertificate

binder := netsec.New(
    netsec.WithLeafResolver(tracker),
    netsec.WithCoalescePolicy(func(handshakeHost, requestHost string) bool {
        // 仅在明确的 vhost 合并关系下放行
        return handshakeHost == "a.example" && requestHost == "b.example"
    }),
    netsec.WithClientCertPolicy(func(host string) bool {
        return host == "secure.example"
    }),
    netsec.WithRejectObserver(func(r *http.Request, e *netsec.IdentityError) {
        log.Printf("rejected reason=%s sni=%q host=%q", e.Reason, e.HandshakeHost, e.RequestHost)
    }),
)

handler := binder.Wrap(reverseProxy)
```

## 测试

- `idna_test.go`：RFC 3492 官方 Punycode 向量、往返、大小写、尾点、IDN、IP、非法 A-label。
- `san_test.go`：通配符层级、部分通配符、IDN U/A-label 等价、IP SAN、无 CN 回退。
- `binder_test.go`：非 TLS、缺/坏 SNI、SAN 不匹配、合并策略、客户端证书跨主机、421 与观察者。
- `tracker_test.go`：SNI 注册/解析、懒解析、`GetCertificate`、`Observe`。
- `e2e_http2_test.go`：基于 `net.Pipe` 的真实 TLS + HTTP/2 多路复用端到端验证（沙箱禁止 socket 监听），含 mTLS 虚拟主机独立握手。
