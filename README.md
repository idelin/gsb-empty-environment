# SSH证书登录限制执行

跳板机用SSH用户证书发放受限登录，攻击者会改选项、冒用主体名或换网段。为Go会话网关实现证书授权包，供认证通过后回调调用：只信给定CA，principals含映射登录名，有效期按半开区间，未知critical option拒绝，source-address匹配真实对端CIDR，force-command固定执行内容并忽略客户端请求。用过期、网段外、未知选项和合法证书验证。

## 用法

```go
ca, err := netsec.ParseAuthorizedKey(caAuthorizedKeyLine)
if err != nil { /* ... */ }

authz, err := netsec.NewAuthorizer(ca,
    // 可选：把网关登录名映射为证书 principal
    netsec.WithLoginMapper(func(login string) (string, error) {
        return directory.Lookup(login)
    }),
)

// SSH 握手（已验证客户端持有证书内嵌私钥）之后回调：
decision, err := authz.Authorize(netsec.Request{
    RawCertificate:   peerCertWireBytes,
    Login:            sshLoginName,
    RemoteAddr:       conn.RemoteAddr(), // 真实传输层对端
    RequestedCommand: clientCommand,     // 有 force-command 时会被忽略
})
if err != nil {
    // errors.Is 可匹配 ErrCertificateExpired / ErrSourceAddressDenied /
    // ErrUnknownCriticalOption / ErrPrincipalMismatch / ErrUntrustedCA ...
}

exec.Command("/bin/sh", "-c", decision.Command) // decision.ForceCommand 为 true 时内容固定
```

## 策略要点

- **单 CA 信任**：只接受构造时给定 CA 的签名，篡改即拒绝。
- **主体名**：登录名经 `LoginMapper` 映射后必须出现在 `principals` 中。
- **半开有效期**：`ValidAfter <= now < ValidBefore`，到期当刻即失效。
- **未知 critical option 拒绝**：仅识别 `force-command` 与 `source-address`。
- **网段匹配**：用真实对端地址匹配 `source-address` 中的 CIDR（裸 IP 视为单主机前缀），IPv4/IPv6 族隔离。
- **强制命令**：存在 `force-command` 时执行内容固定，忽略客户端请求的命令。
- 支持 Ed25519、RSA（仅 rsa-sha2-256/512，拒绝 SHA-1）、ECDSA P-256/384/521；仅依赖 Go 标准库。

## 测试

```sh
go test ./...
```

覆盖合法证书、过期/未生效/到期当刻、网段外与 v4/v6、未知 critical option、
主体名不匹配、外来 CA 与篡改、主机证书拒绝、RSA/ECDSA CA 等场景。
