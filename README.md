# OPC UA ActivateSession 用户令牌准入

本包提供 OPC UA 服务器在 `ActivateSession` 中调用的准入校验，失败时一律拒绝激活。实现不依赖第三方库。

## 校验规则

- `PolicyID` 必须精确匹配端点 `UserTokenPolicies` 中已声明的策略，且策略的 `TokenType` 必须与令牌类型一致。重复且冲突的策略 ID 按失败关闭处理。
- `UserName` 令牌的有效安全策略为 `UserTokenPolicy.SecurityPolicyURI`；该值为空时继承端点（通道）的 `SecurityPolicyURI`。
- 口令令牌的 `EncryptionAlgorithm` 必须与上述有效策略完全相同。若两者都为 `None`（包括空字符串归一化为 `None`），说明口令是明文，返回 `ErrPlaintextPassword`。
- `Certificate` 令牌的签名必须用令牌证书验证，签名数据严格为 `服务器证书 || nonce`。签名不覆盖 nonce 或服务器证书、顺序错误、附带分隔符都会失败。
- 证书令牌成功激活后会原子消费 nonce；同一 nonce 再次出现（即使更换客户端证书并重新签名）返回 `ErrNonceReused`。
- 若同时提供 `ClientCertificate`（`CreateSession` 建立的客户端证书），它必须与令牌证书一致。

本包只做令牌策略与签名准入，不校验口令正确性、证书信任链或证书有效期，这些仍由服务器认证层负责。

## 接入方式

```go
guard := netsec.NewGuard(netsec.Endpoint{
	SecurityPolicyURI: channelSecurityPolicy, // 当前通道的 SecurityPolicyUri
	ServerCertificate: serverCertificateDER,  // 端点服务器证书 DER
	UserTokenPolicies: []netsec.UserTokenPolicy{
		{
			PolicyID:          "username",
			TokenType:         netsec.TokenTypeUserName,
			SecurityPolicyURI: netsec.SecurityPolicyBasic256Sha256,
		},
		{
			PolicyID:          "certificate",
			TokenType:         netsec.TokenTypeCertificate,
			SecurityPolicyURI: netsec.SecurityPolicyBasic256Sha256,
		},
	},
})

err := guard.ActivateSession(netsec.ActivateSessionRequest{
	PolicyID:  token.PolicyID, // UserNameIdentityToken.PolicyId / X509IdentityToken.PolicyId
	TokenType: tokenType,

	// UserNameIdentityToken
	UserName:            userName,
	Password:            encryptedPassword,
	EncryptionAlgorithm: token.EncryptionAlgorithm,

	// X509IdentityToken + ActivateSession.clientSignature
	Certificate:        tokenCertificateDER,
	ClientCertificate:  sessionClientCertificateDER,
	SignatureAlgorithm: clientSignature.Algorithm,
	Signature:          clientSignature.Signature,

	// CreateSession 响应中的 ServerNonce
	ServerNonce: serverNonce,
})
```

可通过 `errors.Is` 区分拒绝原因，例如：

```go
switch {
case errors.Is(err, netsec.ErrPlaintextPassword):
	// 口令令牌未加密
case errors.Is(err, netsec.ErrPolicyNotFound):
	// PolicyId 未声明
case errors.Is(err, netsec.ErrNonceReused):
	// 证书签名 nonce 被重放
case err != nil:
	// 其他准入失败
}
```

多端点或多次请求应共享一个 `*netsec.NonceStore`：

```go
guard := netsec.NewGuardWithNonceStore(endpoint, sharedNonceStore)
```

只对单次请求做一次性校验可使用 `netsec.ValidateActivateSession(endpoint, request, sharedNonceStore)`。

## 测试

```bash
go test ./...
go test ./... -race
```

测试覆盖 None/None 明文口令、加密算法不一致、伪造 `PolicyID`、令牌类型不匹配、证书签名遗漏服务器证书或 nonce、nonce 跨证书复用，以及并发下 nonce 只能成功消费一次。
