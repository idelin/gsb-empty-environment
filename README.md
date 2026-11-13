# 签名方案与密钥不符

证书是RSA密钥，签名方案却写成ECDSA。方案必须与末端公钥的算法和参数一致；不一致不进入验签。RSA证书配rsa_pss_rsae_sha256且签名正确时接受。可导入的Go库供TLS栈调用。保持RSA证书，把方案改成ecdsa_secp256r1_sha256。

## 用法

`CheckSignatureScheme` 只检查方案与末端公钥算法及参数是否一致。
`VerifySignature` 先执行同一检查，通过后才进行密码学验签。

```go
err := netsec.VerifySignature(leafCert, tls.PSSWithSHA256, signed, signature)
if errors.Is(err, netsec.ErrSignatureSchemeMismatch) {
	// 方案与证书公钥不匹配，未进入验签。
}
```
