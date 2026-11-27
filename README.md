点对点加密通道宿主实现 Noise Protocol Framework（Revision 34）的 `Noise_XX`、`Noise_KK` 及降级回退模式 `Noise_XXfallback` 时，若在切换回退模式时未保留前序 `prologue` 与远程临时公钥（`re`）进转录哈希 `h`、忽略全零 DH 输出（低阶点攻击），或在 `Split()` 后继续复用握手密钥或越界使用 `2^64-1` nonce，攻击者可发动密钥泄露冒充（KCI）或中间人模式降级攻击。为 Go 加密通信宿主实现可导入的 Noise 协议状态机（`25519` + `ChaChaPoly` + `SHA256`）与握手安全验证包，严禁联网与严禁依赖外部 Noise 库（仅用 Go 标准库 `crypto/ecdh`、`crypto/sha256`、`crypto/hmac` 与内置/标准 AEAD）。组件严格实现 `CipherState`（64 位递增 nonce、`Rekey()` 及到达 `MaxUint64` 硬拒绝）、`SymmetricState`（`InitializeSymmetric`、`MixKey`、`MixHash`、`EncryptAndHash`、`DecryptAndHash`、`Split`）与 `HandshakeState` 词法令牌执行器（`e`/`s`/`ee`/`es`/`se`/`ss`）：强制在每次 X25519 DH 运算后校验结果非全零；实现 `XXfallback` 回退转换，精确验证首条失败 `IK` 消息的临时公钥被正确注入 `pre-message` 转录链；并在 `Split()` 后立即销毁握手对称状态与临时私钥。另内置协议安全属性断言测试：用确定性测试向量核对 `XX`/`KK`/`XXfallback` 互操作，并构造低阶点注入、篡改 `prologue`、跨模式降级与 nonce 耗尽攻击样本全部拒收。

## 实现

根包 `noise` 仅依赖 Go 标准库，包含：

- 无第三方依赖的 RFC 8439 ChaCha20-Poly1305，用于 Noise 的 `ChaChaPoly`。
- `CipherState`：64 位 little-endian nonce、`Rekey()`、`MaxUint64` 硬拒绝，以及认证失败不推进 nonce。
- `SymmetricState`：`InitializeSymmetric`、`MixKey`、`MixHash`、`EncryptAndHash`、`DecryptAndHash`、`Split`；`Split` 后清零 `ck`/`h`/握手密钥。
- `HandshakeState`：`e`/`s`/`ee`/`es`/`se`/`ss` 令牌执行器，支持 `XX`、`KK`、`IK` 和 `XXfallback`。
- 每次 X25519 DH 后显式检查共享密钥非全零。
- `XXfallback` 从首条 `IK` 消息提取 `e`，按“原始 prologue -> responder 静态 pre-message -> initiator e”顺序重建转录链。
- `Split()` 成功后握手状态、临时私钥和本地静态密钥副本立即销毁；后续握手调用返回 `ErrStateDestroyed`。

## 用法

```go
initStatic, _ := noise.GenerateKeyPair(nil)
respStatic, _ := noise.GenerateKeyPair(nil)

initiator, _ := noise.NewHandshake(noise.Config{
    Pattern:     noise.PatternXX,
    Initiator:   true,
    Prologue:    []byte("application context"),
    LocalStatic: initStatic,
    ExpectedRemoteStatic: respStatic.PublicKey(),
})
responder, _ := noise.NewHandshake(noise.Config{
    Pattern:     noise.PatternXX,
    Initiator:   false,
    Prologue:    []byte("application context"),
    LocalStatic: respStatic,
})

first, _ := initiator.WriteMessage(nil)
_, _ = responder.ReadMessage(first)
second, _ := responder.WriteMessage(nil)
_, _ = initiator.ReadMessage(second)
third, _ := initiator.WriteMessage(nil)
_, _ = responder.ReadMessage(third)

sendI, recvI, _ := initiator.SplitForTransport()
sendR, recvR, _ := responder.SplitForTransport()
packet, _ := sendI.EncryptWithAd(nil, []byte("transport payload"))
plaintext, _ := recvR.DecryptWithAd(nil, packet)
_, _ = recvI, plaintext
```

回退时使用 `FallbackInitiator()` 或 `NewXXFallbackResponder(...)`/`FallbackResponder(...)`；转换会丢弃 IK 对称状态，只保留原始 prologue、对端临时公钥和必要的密钥材料。

## 验证

```sh
go test ./...
go vet ./...
```

测试覆盖 RFC 8439 ChaCha20/Poly1305 向量、确定性 `XX`/`KK`/`XXfallback` 互操作向量、低阶点注入、篡改 prologue、跨模式降级、IK 临时公钥转录校验、`Split` 清理和 nonce 耗尽拒绝。
