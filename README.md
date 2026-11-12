# TLS恢复票据配置绑定

服务配置收紧后，旧恢复票据不能跳过新的客户端认证要求。实现供Go接入宿主调用的票据封装包，标准库AEAD绑定租户、协议选项、认证等级和策略摘要；恢复必须先比较当前策略是否允许原等级，不能仅验证密文。用真实票据测试跨租户、宽限期换钥、配置变更及验证失败不生成会话。

## 设计

- `Policy` 描述租户当前安全配置（最低认证等级、TLS 版本/ALPN、票据 TTL），
  `Policy.Digest()` 生成与切片顺序无关的 SHA-256 策略摘要，`Policy.Allows()`
  判断认证等级与协议选项是否仍被当前策略接受。
- `Keyring` 基于标准库 AES-GCM 管理票据密钥；`Rotate` 换钥后旧密钥进入
  只读宽限期，到期自动淘汰。
- 票据信封为 `version | key_id | nonce | ciphertext+tag`，AAD 绑定
  域名、租户和密钥标识；明文 claims 绑定租户、协议选项、认证等级、
  策略摘要、签发/过期时间和宿主会话数据。
- `Manager` 供宿主直接调用。恢复校验顺序为 AEAD 认证 → 租户一致性 →
  有效期 → `Policy.Allows`，全部通过后才调用 `SessionFactory`，
  任何失败都返回哨兵错误且不生成会话；策略摘要变化但等级仍被允许时，
  返回的 `Session.PolicyChanged` 为 true。

## 接入示例

```go
keyring, err := netsec.NewKeyring(key, now, 24*time.Hour/time.Second)
if err != nil { /* ... */ }

manager, err := netsec.NewManager(keyring, policyProvider, sessionFactory)
if err != nil { /* ... */ }

ticket, err := manager.Issue(tenant, netsec.AuthCertificate, options, sessionData)
if err != nil { /* 策略不允许该等级时为 ErrPolicyRejectsLevel */ }

session, err := manager.Resume(tenant, ticket)
switch {
case errors.Is(err, netsec.ErrPolicyRejectsLevel):
    // 配置已收紧，要求客户端重新完成认证
case err != nil:
    // 跨租户、过期、篡改或密钥超宽限期等
default:
    _ = session // 仅在全部安全检查通过后才会生成
}
```

## 测试

测试全部使用 `crypto/rand` 生成的真实 AES-256 密钥与真实票据，配合可
 推进的假时钟，覆盖：

- 跨租户恢复被 AAD/显式租户校验拒绝，且不调用会话工厂；
- 宽限期内旧钥票据可恢复、换钥后新票据走新钥、超宽限期旧票据失效；
- 策略收紧（认证等级、TLS 版本、ALPN）后旧票据返回
  `ErrPolicyRejectsLevel`，不能跳过新认证要求；
- 票据篡改、截断、版本错误、过期等验证失败路径均不生成会话；
- 策略摘要的确定性、顺序无关性与配置敏感性；
- 并发签发/恢复竞态检测（`go test -race`）。

运行：

```sh
go vet ./...
go test -race -count=1 ./...
```
