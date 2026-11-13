# netsec JWT 验证库

供资源服务导入的严格 JWT 验签库。

安全规则：

- 调用方必须显式列出允许的非 `none` 算法，没有默认算法。
- `none`（不区分大小写）、空 `alg`、缺失 `alg` 一律失败。
- 空签名一律失败，列出的算法必须通过真实密码学验签。
- 算法名区分大小写，且只能使用白名单中的标准算法。

支持 `HS256/384/512`、`RS256/384/512`、`PS256/384/512`、
`ES256/384/512` 和 `EdDSA`。

```go
verifier, err := netsec.NewVerifier(secret, []string{"HS256"})
if err != nil {
	// 配置不安全或密钥类型不匹配
}

claims, err := verifier.Verify(token)
if err != nil {
	// 算法不允许、签名错误或 JWT 格式错误
}
```

也可以单次调用：

```go
claims, err := netsec.VerifyJWT(token, secret, []string{"HS256"})
```

`secret` 对 HMAC 算法必须是 `[]byte`；RSA、ECDSA 和 Ed25519 使用对应的公钥类型。
