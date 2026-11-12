# netsec — SMB3 协商降级拒绝判定包

`netsec` 是一个传输无关的 Go 包，为 SMB 3.x 服务端在协商阶段提供降级
攻击防护。调用方把每条完整入站报文喂给 `Engine`，再按返回的 `Decision`
执行传输动作即可，包本身不做任何网络 I/O 或加解密。

## 强制策略

- **算法协商（SMB 3.1.1）**：通过 `ENCRYPTION_CIPHERS` 协商上下文选算法，
  客户端算法列表与服务端偏好无交集时返回 `ActionReject`。
- **加密强制**：服务器或目标共享要求加密时，方言低于 SMB 3.0、没有可用
  算法、或 `TREE_CONNECT` 之后出现任何明文请求，一律拒绝并断开。
- **预认证完整性哈希**：引擎自行累计，初值为 64 个零字节，对完整协商请求
  与响应依次计算 `H = SHA-512(H ‖ 报文字节)`。
- **会话签名验证**：`SESSION_SETUP` 的签名交由调用方注入的
  `SessionSetupVerifier`，用当前哈希校验，失败即断开。
- **重复协商**：协商完成后再收到 `NEGOTIATE` 立即断开。

`ActionReject` 表示“先回协议错误再关闭”，`ActionDisconnect` 表示“直接关闭、
不再发送任何数据”；两者都会把引擎置为终态。

## 用法

```go
engine := netsec.NewEngine(netsec.Config{
    RequireEncryption: true,
    Ciphers:           []uint16{netsec.CipherAES128GCM, netsec.CipherAES128CCM},
    VerifySessionSetup: func(sessionSetup, preauthHash []byte) bool {
        return verifySignature(sessionSetup, preauthHash)
    },
    ShareRequiresEncryption: func(sharePath string) bool {
        return sharePath == `\\srv\secret`
    },
})

if d := engine.HandleRequest(negotiateRequest); d.Action != netsec.ActionAllow {
    return d // 按 d.Action 拒绝或断开
}
engine.ObserveNegotiateResponse(negotiateResponseBytes) // 累计响应哈希

for message := range inbound {
    d := engine.HandleRequest(message)
    switch d.Action {
    case netsec.ActionAllow:
        // 继续正常 SMB2 处理
    case netsec.ActionReject:
        sendErrorThenClose(d.Err)
    case netsec.ActionDisconnect:
        closeConnection()
    }
}
```

## 测试

`go test ./...` 覆盖降级方言、算法列表篡改、哈希不符、未加密请求，以及
重复 `NEGOTIATE`、乱序命令、未知协议、无算法的加密变换报文等边界。
