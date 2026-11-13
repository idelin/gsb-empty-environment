# CredSSP版本降级与公钥绑定

远程桌面的凭据委托在版本协商时，攻击者做中间人，把版本压到旧值，使公钥绑定里不含服务端随机数，从而可以重放。为Go远程访问客户端写可导入的 CredSSP 绑定包，加密与哈希由回调提供：客户端取本端最高版本与服务端回复版本的较小值，低于调用方策略的最低版本（默认 5）时中止；版本 2 到 4 时 pubKeyAuth 内容是服务端证书公钥字节本身，版本 5 及以上是 SHA-256("CredSSP Client-To-Server Binding Hash" 加一个零字节 ‖ clientNonce ‖ 服务端公钥)，clientNonce 为 32 字节且每次握手新生成；服务端回复的校验值，版本 5 及以上是 SHA-256("CredSSP Server-To-Client Binding Hash" 加一个零字节 ‖ clientNonce ‖ 公钥)，其余版本是公钥首字节加一后接其余字节；校验用常量时间比较，不一致就中止，且不得发送凭据；服务端回复里的版本在握手中途变化时中止。用降级到版本 2、改写公钥、复用 clientNonce、校验值不符和版本中途变化验证。

## 包结构

可导入子包 `credssp`（导入路径 `example.org/netsec/q0633/credssp`）只负责
绑定状态机，不做传输、ASN.1 或自带密码学，加解密与哈希通过 `credssp.Crypto`
回调注入。

## 握手流程

1. `credssp.New(&credssp.Config{Crypto: cb})` 校验策略并生成 32 字节随机
   `clientNonce`，每个连接必须新建握手。
2. `Negotiate(serverVersion)` 取 `min(客户端最高版本, 服务端版本)`；低于
   `MinVersion`（零值默认 5）返回包装了 `ErrDowngrade` 的错误并中止握手。
3. `ClientBinding(serverPublicKey)` 返回经 `Crypto.Encrypt` 封装的
   pubKeyAuth 内容：v2–v4 是公钥 DER 字节本身，v5+ 是
   `Hash("CredSSP Client-To-Server Binding Hash" || 0x00 || nonce || 公钥)`。
4. 每条服务端 `TSRequest` 都可先用 `CheckServerVersion` 比对首次协商版本，
   版本变化返回 `ErrVersionChanged` 并中止。
5. `VerifyServerBinding(serverVersion, encryptedProof)` 内部先做版本检查，
   再经 `Crypto.Decrypt` 解密并以 `crypto/subtle` 常量时间比对期望值：
   v5+ 为服务端方向绑定哈希，v2–v4 为公钥首字节加一、其余字节不变。
6. 只有校验通过进入 `StateVerified` 后，`SealCredential` 才会经回调加密
   凭据；任何中止后的调用都返回 `ErrAborted`，保证不发送凭据。

## 用法示例

```go
import "example.org/netsec/q0633/credssp"

h, err := credssp.New(&credssp.Config{Crypto: myCrypto}) // SHA-256 + 会话密钥加解密
if err != nil { /* 配置错误 */ }

if err := h.Negotiate(serverTSRequest.Version); err != nil {
    return err // 典型为 ErrDowngrade / ErrUnsupportedVersion
}
pubKeyAuth, err := h.ClientBinding(serverCert.PublicKeyDER())
if err != nil { return err }

// 对后续每条服务端 TSRequest：
if err := h.CheckServerVersion(serverTSRequest.Version); err != nil {
    return err
}
if err := h.VerifyServerBinding(serverTSRequest.Version,
    serverTSRequest.PubKeyAuth); err != nil {
    return err // ErrMismatch 或 ErrVersionChanged，凭据不会发出
}
authInfo, err := h.SealCredential(credentialBytes)
```

## 测试

`go test ./...`（含 `-race`）覆盖：降级到 v2 被默认策略拒绝、公钥被改写后
校验失败、不同握手的 clientNonce 与 v5 绑定不可复用、校验值不符即中止且
拒绝凭据、服务端版本中途变化即中止，以及 v2–v4 旧版绑定的正确流程。
