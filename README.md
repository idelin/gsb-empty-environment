# Fulcio 证书扩展与工作流身份绑定

Fulcio 签发的证书用私有扩展 OID `1.3.6.1.4.1.57264.1.x` 记录 OIDC
颁发者、构建签名者、源仓库、提交摘要、引用与触发事件。验证者只看 SAN
时，另一个仓库里同名的工作流也可能通过校验。本包从证书扩展提取完整构建
身份，并要求调用方给出的每一项策略都精确匹配。

## 使用

```go
policy := netsec.Policy{
	Issuer:                 "https://token.actions.githubusercontent.com",
	BuildSignerURI:         "https://github.com/acme/app/.github/workflows/release.yml@refs/tags/v1.2.3",
	SourceRepositoryURI:    "https://github.com/acme/app",
	SourceRepositoryDigest: "sha256:0123...",
	SourceRepositoryRef:    "refs/tags/v1.2.3",
	BuildTrigger:           "push",
	RequireTagRef:          true,
}

identity, err := netsec.VerifyIdentity(cert, policy)
```

`ExtractIdentity` 只做扩展解析与内部一致性校验；`VerifyIdentity` 在解析后
执行策略比较。策略中非空的期望值都是必需的，扩展缺失返回
`ErrMissingExtension`，值不一致返回 `ErrPolicyMismatch`。`Policy.Require`
可要求某个扩展存在但不限定其值。空策略会被拒绝，不会被当成“全部接受”。

## OID

| OID | 含义 | 编码 |
| --- | --- | --- |
| `.1.1` | 旧版颁发者 | 原始字节 |
| `.1.8` | 颁发者 | DER `UTF8String` |
| `.1.9` | 构建签名者 | URI 原始字节 |
| `.1.12` | 源仓库 | URI 原始字节 |
| `.1.13` | 源仓库摘要 | 原始字节 |
| `.1.14` | 源仓库引用 | 原始字节 |
| `.1.20` | 构建触发事件 | 原始字节 |

## 拒绝条件

- 同名工作流位于不同仓库：构建签名者、源仓库等策略值不匹配。
- `.1.1` 与 `.1.8` 同时存在但值不同。
- 六个身份 OID 中任一 OID 重复出现。
- 策略要求但证书缺少的扩展，或扩展值为空、编码非法。
- URI SAN 不存在，或与 `.1.9` 构建签名者不一致。
- 策略要求标签（`RequireTagRef`），但引用是 `refs/heads/...` 等非标签引用。

当 `.1.8` 不存在时，`.1.1` 可作为旧证书的颁发者回退值；两者同时存在时
必须完全一致。

## 测试

```sh
go test -v ./...
```
