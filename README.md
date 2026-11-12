# iSCSI双向CHAP反射防护

iSCSI 双向 CHAP 里，攻击者可开第二条连接把目标发出的挑战反射回去，换取目标的响应来冒充发起端，或在两个方向使用相同密钥。为Go目标端写供登录阶段调用的 CHAP 认证包：要求双向认证，发起端回传的挑战不得与本端在任何并发连接中发出的挑战相同，挑战长度至少 16 字节、不超过 1024 字节，配置里的正反向密钥必须不同，响应按 MD5(标识‖密钥‖挑战) 计算并支持 0x 十六进制与 base64 两种编码。用反射挑战、跨连接反射、短挑战、同密钥配置和错误编码验证。

## 使用

`Authenticator` 保存所有连接共享的挑战注册表；每个登录连接创建一个
`Session`，连接结束时必须调用 `Close` 释放注册项。

```go
auth, err := netsec.NewAuthenticator(netsec.Config{
    InitiatorSecret: "forward-secret",
    TargetSecret:    "reverse-secret",
    ChallengeLength: 32, // 0 使用默认值；允许范围 16..1024
})
if err != nil {
    return err
}

session := auth.NewSession()
defer session.Close()

// 正向：目标挑战发起端。
challenge, err := session.ChallengeInitiator()
if err != nil {
    return err
}
// 将 challenge.Identifier/challenge.Bytes() 发给发起端。

// 发起端响应后，按 0x 十六进制或 base64 解码并校验。
if err := session.VerifyInitiatorResponse(identifier, encodedResponse); err != nil {
    return err
}

// 反向：校验发起端的挑战并生成目标响应。
reply, err := session.HandleInitiatorChallenge(identifier, reverseChallenge)
if err != nil {
    return err
}
// reply.Hex() 或 reply.Base64() 发给发起端。
```

反向挑战若等于本认证器在任一仍在线的连接上发出的挑战，返回
`ErrReflectedChallenge`。零长度或超出 16..1024 字节的挑战返回
`ErrInvalidChallengeLength`；正反向密钥相同返回 `ErrSameSecrets`；非法
`0x`/base64 数据返回 `ErrInvalidEncoding`。
