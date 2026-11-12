# RADIUS Message-Authenticator强制

针对RADIUS的碰撞类攻击会伪造Access-Accept，请求和响应都需可验证的Message-Authenticator。为Go RADIUS服务端实现报文校验包，供UDP接收循环调用：Access-Request中它须为首个属性并按HMAC-MD5验证，缺失、重复或长度非16字节拒绝；响应用请求认证符计算，客户端丢弃缺失或不匹配的响应。用被篡改属性、缺字段和合法交换验证。

## 使用

UDP 接收循环中先校验 Access-Request，再构造响应：

```go
req, err := netsec.VerifyRequest(packet, sharedSecret)
if err != nil {
    // 丢弃报文：缺少、重复、非首属性、长度错误或 HMAC 不匹配。
    return
}

resp, err := netsec.NewResponse(
    netsec.CodeAccessAccept,
    req.Identifier,
    req.Authenticator[:],
    attributes,
    sharedSecret,
)
```

客户端收到响应时必须丢弃校验失败的结果：

```go
_, err = netsec.VerifyResponse(packet, requestAuthenticator, sharedSecret)
if err != nil {
    // 丢弃响应。
}
```

已有响应报文可调用 `SignResponse` 填充 Message-Authenticator 和 Response
Authenticator；该响应必须已经包含一个 16 字节的 Message-Authenticator 属性。
`NewResponse` 会自动把它放在属性列表首位。
