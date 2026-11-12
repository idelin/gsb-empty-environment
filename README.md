# REQUIRETLS逐跳降级拒绝

邮件带 REQUIRETLS 要求全程加密，攻击者可剥离 STARTTLS、在明文连接上伪造该参数，或让中继在下一跳降级。给Go邮件中继写供出站投递调用的组件，按 RFC 8689：只有在已建立 TLS 的入站会话里才承认 MAIL FROM 的 REQUIRETLS 参数，明文会话里出现则拒绝该命令；出站时下一跳须在 STARTTLS 之后的 EHLO 里声明 REQUIRETLS，且证书经调用方注入的校验通过，任一条件不满足就把邮件作为永久失败退回，不降级也不换其他 MX 明文重试；邮件头里的 TLS-Required: No 只能由发件人在不带 REQUIRETLS 时使用。用明文伪造参数、剥离 STARTTLS、证书校验失败和合法链路验证。

## 用法

入站服务器只在 STARTTLS 完成后接受 `REQUIRETLS`，并在 DATA 阶段核对
`TLS-Required` 头：

```go
srv := &netsec.InboundServer{
    TLSConfig: tlsConfig,
    Handler: func(msg *netsec.InboundMessage) error {
        // msg.TLS 为 true，msg.RequireTLS 表示信封要求全程 TLS。
        return queue(msg)
    },
}
_ = srv.ServeListener(listener)
```

出站投递时由调用方注入证书校验。返回 `IsPermanent(err) == true` 时生成
永久失败退信，不得改走明文或降级重试其他 MX：

```go
d := &netsec.Deliverer{
    VerifyCertificate: func(state tls.ConnectionState) error {
        return verifyPeer(state.PeerCertificates[0]) // 包含主机名校验
    },
}
err := d.Deliver(ctx, netsec.Target{
    Address:    "mx.example.com:25",
    ServerName: "mx.example.com",
}, &netsec.Envelope{
    MailFrom:   "sender@example.org",
    RcptTo:     []string{"recipient@example.net"},
    Data:       message,
    RequireTLS: true,
})
```

`DeliverAll` 只在暂时性失败时尝试下一个 MX；任何 TLS/REQUIRETLS 策略失败
都会立即返回永久失败。`PrepareMessage` / `ValidateTLSRequirement` 可供
其他投递路径复用头部不一致检查。

## 验证

`go test ./...` 覆盖：

- 明文会话伪造 `MAIL FROM ... REQUIRETLS`：5xx 拒绝，事务不开启；
- 下一跳没有 STARTTLS 或拒绝 STARTTLS：永久失败，未发送 MAIL；
- STARTTLS 后证书校验失败：永久失败，未发送 MAIL；
- STARTTLS 后 EHLO 未声明 REQUIRETLS：永久失败，未发送 MAIL；
- 入站 TLS + 出站 TLS/REQUIRETLS 的完整合法链路；
- `TLS-Required: No` 与 `REQUIRETLS` 冲突、重复/非法头部值。
