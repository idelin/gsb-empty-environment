# TURN凭据与对端约束

可导入的 Go TURN 鉴权包（仅依赖标准库）。它解决三类问题：篡改用户名里的过期时间或租户段、伪造 `MESSAGE-INTEGRITY`、以及把中继目标指向回环或私网。

## 凭据规则

- 用户名固定为 `expiry:tenant:user`，必须恰好三段，`expiry` 为十进制 Unix 秒，`tenant` 和 `user` 非空，`user` 不能含冒号。
- 默认密码派生：`base64(HMAC-SHA1(shared_secret, username))`。
- `MESSAGE-INTEGRITY` 密钥：`MD5(username:realm:password)`，属性本身用 HMAC-SHA1。
- 用户名整串参与密码派生，因此改动过期时间、租户或用户都会使 `MESSAGE-INTEGRITY` 校验失败。
- `AuthenticateSTUN` 先验证完整性，再信任过期时间、租户和 `PEER-ADDRESS` / `XOR-PEER-ADDRESS` 内容。
- `MESSAGE-INTEGRITY` 之后只允许 `FINGERPRINT`；其他后置属性会被拒绝，防止绕过签名夹带内网对端。

## 基本用法

```go
validator, err := netsec.NewValidator(netsec.Config{
    SharedSecret:   []byte(secret),
    Realm:          "turn.example.org",
    AllowedTenants: []string{"acme"}, // 为空表示接受任意合法租户
})
if err != nil {
    return err
}

credentials, err := validator.AuthenticateSTUN(rawSTUNRequest)
if err != nil {
    // errors.Is(err, netsec.ErrIntegrityMismatch)
    // errors.Is(err, netsec.ErrUsernameExpired)
    // errors.Is(err, netsec.ErrTenantNotAllowed)
    // errors.Is(err, netsec.ErrForbiddenPeer)
    return err
}
```

`PeerPolicy` 零值即严格策略：拒绝回环、RFC 1918、IPv6 ULA、CGNAT（100.64/10）、链路本地、组播、未指定和保留地址，并会把 `::ffff:10.0.0.1` 这类 IPv4-mapped IPv6、6to4/Teredo 中嵌入的 IPv4 归一化后检查。测试或隔离环境可通过 `AllowLoopback`、`AllowPrivate`、显式 `Allowed` CIDR 或 `AllowUnsafe` 放宽。

完整用法见 `credentials.go` 与 `stun.go` 的导出 API。`ParseUsername` 只做结构解析；接受线上请求时必须调用 `AuthenticateSTUN` / `AuthenticateMessage`，否则无法证明用户名未被篡改。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖 RFC 5769 `MESSAGE-INTEGRITY` 向量、过期/租户/用户篡改、错误共享密钥、重复属性、签名后夹带属性，以及 IPv4/IPv6 回环、私网、CGNAT、链路本地和 IPv4-mapped 对端。
