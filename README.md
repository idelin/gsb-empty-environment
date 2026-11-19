# CredSSP版本降级与公钥绑定

远程桌面的凭据委托在版本协商时，攻击者做中间人，把版本压到旧值，使公钥绑定里不含服务端随机数，从而可以重放。为Go远程访问客户端写可导入的 CredSSP 绑定包，加密与哈希由回调提供：客户端取本端最高版本与服务端回复版本的较小值，低于调用方策略的最低版本（默认 5）时中止；版本 2 到 4 时 pubKeyAuth 内容是服务端证书公钥字节本身，版本 5 及以上是 SHA-256("CredSSP Client-To-Server Binding Hash" 加一个零字节 ‖ clientNonce ‖ 服务端公钥)，clientNonce 为 32 字节且每次握手新生成；服务端回复的校验值，版本 5 及以上是 SHA-256("CredSSP Server-To-Client Binding Hash" 加一个零字节 ‖ clientNonce ‖ 公钥)，其余版本是公钥首字节加一后接其余字节；校验用常量时间比较，不一致就中止，且不得发送凭据；服务端回复里的版本在握手中途变化时中止。用降级到版本 2、改写公钥、复用 clientNonce、校验值不符和版本中途变化验证。

## 使用

根包 `example.org/netsec/q0633` 可直接导入。`Client.Begin` 完成版本协商、
生成 32 字节 nonce 并返回 `pubKeyAuth`；`Client.Complete` 校验服务端版本和
绑定值，只有校验成功后才调用 `SendCredentials`。

```go
client, err := netsec.NewClient(netsec.Config{
    MaxVersion: 6,
    MinVersion: 5, // 零值同样默认为 5，旧版本必须显式放开
    Hash: func(input []byte) []byte {
        sum := sha256.Sum256(input)
        return sum[:]
    },
    Random: rand.Reader,
    SendCredentials: func(ctx context.Context) error {
        // 在这里加密并发送凭据。
        return sendEncryptedCredentials(ctx)
    },
})
if err != nil {
    return err
}

binding, err := client.Begin(serverVersion, serverPublicKey)
if err != nil {
    return err
}
// 发送 binding.Version、binding.ClientNonce 和 binding.PubKeyAuth。

if err := client.Complete(ctx, netsec.ServerReply{
    Version:  serverVersion,
    Checksum: serverChecksum,
}); err != nil {
    return err
}
```

`Hash` 和 `Random` 为 `nil` 时分别使用 `crypto/sha256` 和
`crypto/rand.Reader`。`SendCredentials` 负责凭据的加密与传输；版本降级、
版本中途变化、nonce 复用或绑定值不匹配都会在调用它之前中止握手。

`ComputePubKeyAuth`、`ComputeServerChecksum` 和 `VerifyServerChecksum` 也
可作为无状态原语单独使用。版本 2 到 4 的绑定不包含 nonce，默认策略会拒绝；
只有调用方显式设置 `MinVersion` 为 2 到 4 时才会启用。

## 验证

```sh
go test ./...
```

测试覆盖降级到版本 2、公钥替换、旧校验值重放、nonce 复用、校验值不匹配、
服务端版本中途变化，以及成功握手的凭据发送时机。
