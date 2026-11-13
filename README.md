# 无证明拒绝约束令牌

`netsec` 是一个可被资源服务直接导入的 Go HTTP 中间件。它在验证访问令牌签名的同时强制检查令牌的 `cnf` 约束：

- 令牌不含 `cnf`：按普通 bearer 令牌规则验证和放行。
- 令牌含 `cnf.jkt`：必须提供与该 JWK SHA-256 指纹匹配的 `DPoP` 证明。
- 令牌含 `cnf["x5t#S256"]` 或 `cnf.x5t`：必须提供指纹匹配的 TLS 客户端证书。
- 令牌含 `cnf` 但缺少证明/证书：返回 `401`，绝不降级成 bearer。
- `cnf` 为空、格式错误或不含受支持的绑定：同样拒绝。

## 使用

```go
authenticator, err := netsec.New(netsec.Config{
	HMACSecret: []byte(os.Getenv("ACCESS_TOKEN_SECRET")),
	// 或使用 PublicKey: rsaPublicKey / ecdsaPublicKey / ed25519.PublicKey
})
if err != nil {
	log.Fatal(err)
}

mux := http.NewServeMux()
mux.HandleFunc("/items", listItems)
http.ListenAndServe(":8080", authenticator.Middleware(mux))
```

也可以用于中间件链：

```go
handler := netsec.NewMiddleware(cfg)(mux)
```

认证成功后，资源处理函数可以从上下文读取主体和证明信息：

```go
identity, ok := netsec.IdentityFromContext(r.Context())
if !ok {
	http.Error(w, "unauthenticated", http.StatusUnauthorized)
	return
}
```

`Identity` 包含 `Subject`、`Claims`、`Confirmation`，以及本次授权使用的 `Proof`（DPoP）或 `Certificate`（mTLS）。

## DPoP 证明

证明放在 `DPoP` 请求头中，访问令牌仍可通过 `Authorization: Bearer <token>` 发送。证明必须：

- 使用非对称算法签名，并在头部携带与 `cnf.jkt` 匹配的公钥 JWK；
- 校验 `htm`、`htu`（不含查询参数和片段）与当前请求一致；
- 如包含 `ath`，其值必须为访问令牌的 SHA-256 base64url 摘要；
- 具有合理的 `iat`，且默认启用内存 `jti` 重放缓存。

## mTLS 证书

资源服务前面终止 TLS 时，可由受信适配器把已经提取的客户端证书放入请求上下文：

```go
req = req.WithContext(netsec.WithClientCertificate(req.Context(), clientCert))
```

直接由 Go 监听 TLS 时，默认读取 `r.TLS.PeerCertificates[0]`。证书指纹支持 base64url、标准 base64 和十六进制表示。

## 验证

```sh
go test ./...
```
