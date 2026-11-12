# ACME IP 标识符规范化与验证

本包实现 RFC 8738 的 ACME `ip` 标识符处理，供签发服务的订单创建与
授权验证阶段调用。

## 订单创建

```go
id, err := netsec.NewIPIdentifier("8.8.8.8")
if err != nil {
	// 非规范写法、私网/保留地址、通配写法等都会在此被拒绝
}

err = netsec.ValidateIdentifiers([]netsec.Identifier{id})
```

`ParseIdentifierJSON` 可直接校验 newOrder 请求中的标识符对象。
IPv4 必须是无前导零的点分十进制；IPv6 必须是 RFC 5952 小写压缩形式。
回环、私网、链路本地、唯一本地、多播、IPv4 映射、共享及 IANA 保留/
文档地址均被拒绝。

## 授权验证

```go
err = id.ValidateChallenge(netsec.ChallengeHTTP01) // dns-01 返回错误

// http-01：直接连接标识符地址，Host 为该地址，跨地址/跨 DNS 重定向拒绝
err = netsec.ValidateHTTP01(ctx, id, token, keyAuthorization, nil)

// tls-alpn-01：SNI 为反向解析名，并校验 iPAddress SAN 与 acmeIdentifier
validator := &netsec.TLSALPN01Validator{}
err = validator.Validate(ctx, id, keyAuthorization)
```

`HTTP01ChallengeURL`、`HTTP01HostHeader`、`ReverseDNSName` 和
`NewTLSALPN01Config` 可用于接入已有验证流程。测试可通过 `http.Client`
的自定义 `Transport` 或 `TLSALPN01Validator.DialContext` 注入本地桩。

运行验证：

```sh
go test ./...
```
