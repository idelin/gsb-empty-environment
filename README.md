# netsec — 发行者绑定的 ID 令牌校验库

`netsec` 是一个可导入的 Go 库（package `netsec`），供 OIDC 客户端校验 ID 令牌（JWT/JWS）。
它修复的问题是：**令牌的 `iss` 是另一家发行者，签名却恰好能用本家缓存的密钥验证通过**。

## 威胁模型

攻击者拿到一个签名合法的令牌，**保持签名密钥不变，只把 `iss` 声明改成另一家发行者**。
如果校验方：

1. 不校验 `iss` 是否等于当前会话选定的发行者；并且
2. 用一个跨发行者共享/全局缓存的密钥集合按 `kid` 取密钥验签；

那么这个伪造令牌就会被错误接受。

## 安全保证

`Verifier` 与**唯一**一个发行者绑定，并强制执行：

- **发行者一致**：`iss` 必须与当前会话选定的发行者逐字符相等，否则返回
  `ErrIssuerMismatch`。该校验在查找任何密钥之前完成，外家发行者根本接触不到本地密钥缓存。
- **密钥来源一致**：只使用该发行者自己的密钥——通过它的 OIDC discovery
  （`/.well-known/openid-configuration`）和同源 JWKS 端点获取，或使用显式绑定到该发行者的
  静态 `KeySet`。每个 `Verifier` 的密钥相互隔离，不存在跨发行者的全局密钥缓存。
- **JWKS 同源锁定**：discovery 返回的 `jwks_uri` 必须与发行者同源（scheme/host/port 一致），
  防止被引导到第三方取密钥。
- 仅接受非对称算法（`RS*`、`PS*`、`ES*`、`EdDSA`），拒绝 `alg: none` 与未知算法。
- 标准声明校验：`exp` / `nbf` / `iat`（带可选 leeway），以及可选的 `aud`、`nonce`。

结论：**只有 `iss` 与密钥来源是同一家时，ID 令牌才会被接受。**

## 用法

动态 discovery（默认）：

```go
import "example.org/netsec/q0160"

verifier, err := netsec.NewVerifier(netsec.Config{
    Issuer:   "https://login.example.org",
    Audience: []string{"my-client-id"},
})
if err != nil { /* ... */ }

token, err := verifier.Verify(ctx, compactJWT)
if errors.Is(err, netsec.ErrIssuerMismatch) {
    // iss 不是本会话选定的发行者
}
```

静态/固定密钥（不发起任何网络请求，密钥被绑定到该发行者）：

```go
keys, err := netsec.ParseKeySet(jwksJSON)
verifier, err := netsec.NewVerifier(netsec.Config{
    Issuer: "https://login.example.org",
    Keys:   keys,
})
```

## 错误

| 错误哨兵 | 含义 |
| --- | --- |
| `ErrMalformedToken` | 不是三段式紧凑 JWS，或编码/结构非法 |
| `ErrIssuerMismatch` | `iss` 缺失或不等于配置的发行者 |
| `ErrKeyNotFound` | 该发行者的密钥集合中没有匹配 `kid` 的密钥 |
| `ErrUnsupportedAlg` | 不支持的 `alg`（含 `none`） |
| `ErrSignature` | 密码学签名验证失败 |
| `ErrClaim` | `exp`/`nbf`/`iat`/`aud`/`nonce` 校验失败 |

## 测试

`go test ./...` 使用进程内的虚拟 OIDC 提供方（不绑定真实端口），覆盖：

- 保持签名密钥、仅改写 `iss` 的攻击被 `ErrIssuerMismatch` 拒绝；
- 用别家密钥签名、`iss` 指向本家的混淆攻击被拒绝；
- 发行者与密钥来源一致的合法令牌正常通过；
- JWKS 跨源、未知 `kid`、`alg:none`、过期/受众/nonce 等情形。
