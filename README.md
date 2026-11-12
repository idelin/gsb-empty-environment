# SPAKE2+验证者份额校验

口令认证验证者只存w0与L，攻击者发送单位元或小阶点作为客户端份额，会使会话密钥与口令无关。为Go协议库实现P-256上SPAKE2+验证者包，供握手服务调用：shareP须是非单位元曲线点，转录绑定上下文、双方身份、shareP、shareV、Z、V与w0，确认MAC按方向分用密钥。用无效点、错误身份、MAC反射和合法握手验证。

## 用法

`spake2plus` 包实现 RFC 9383 验证者（服务端）侧，套件为
SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256：

```go
v, err := spake2plus.NewVerifier(spake2plus.VerifierConfig{
    Context:    []byte("my-protocol/v1"),
    ProverID:   []byte("client"),
    VerifierID: []byte("server"),
    W0:         w0, // 注册输出标量 w0（大端）
    L:          l,  // 注册记录 L = w1*B（SEC 1 点编码）
})
if err != nil { /* 注册记录无效 */ }

shareV, err := v.Start() // 发送 shareV 给客户端

confirmV, sessionKey, err := v.Finish(shareP, confirmP)
// err 为 ErrInvalidShare / ErrDegenerateShare / ErrConfirmation 时中止握手；
// 成功时把 confirmV 发回客户端，sessionKey 作为会话密钥。
```

安全要点：

- `shareP` 必须是合法的非单位元 P-256 点；`shareP == w0*M`（去掩码后为单位元）也会被拒绝，保证会话密钥总是依赖口令派生秘密与验证者临时秘密。
- 转录按 RFC 9383 绑定 Context、双方身份、M、N、shareP、shareV、Z、V 与 w0。
- 确认 MAC 使用方向独立的密钥（K_confirmP / K_confirmV），反射攻击无法通过校验。
- 每个 `Verifier` 实例只跑一次握手，`Finish` 后清除 w0 与临时秘密。

测试：`go test ./...`
