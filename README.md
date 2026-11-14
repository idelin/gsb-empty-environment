# 旧时间戳被附到了新的签名产物

代码签名服务为每个产物申请时间戳，攻击者把早先的令牌附到新产物上，验签方误以为新产物也在证书有效期内签署。请实现供Go签名验证服务调用的可导入时间戳绑定组件。提取令牌中的message imprint、hash算法、策略、序列号和签发者身份，按RFC 3161核对imprint等于待验签名的摘要，并核对令牌证书链以及时间落在签名者证书有效区间内；非cesT类型、多令牌、序列重复和签发者不在信任列表都拒绝。令牌缓存以imprint加签发者身份为键，过期或撤销后不再复用。用旧令牌换新摘要、摘要算法不一致、签名者证书已过期、合法联署和双令牌验证时间只证明它覆盖的那个签名。

## 使用方式

```go
roots := x509.NewCertPool()
roots.AddCert(tsaRoot)

verifier, err := netsec.NewVerifier(netsec.VerifierConfig{
    Roots:            roots,
    TrustedIssuers:   []string{"CN=Production TSA"},
    RevocationChecker: revocationList,
})
if err != nil {
    return err
}

// signature 是被时间戳覆盖的代码签名值。
binding, err := verifier.Verify(signature, timestampToken, signerCertificate)
```

`Verify` accepts exactly one token. Use `VerifyAll` for independent
signature/token pairs; each binding keeps the timestamp that covers that
signature. `ParseTimestampToken` extracts the imprint, hash OID, policy, serial,
generation time, TSA identity and co-signers while checking the CMS signature.

Successful bindings are cached by `(message imprint, TSA identity)`. Cache
entries are discarded after their TTL or certificate deadline, and every reuse
checks the configured revocation source. Reusing a TSA serial for a different
token is rejected.
