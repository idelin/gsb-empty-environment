# 属性证书持有者绑定

本包解析并核验 RFC 5755 AttributeCertificate。角色判定应先调用
`VerifyAttributeCertificate`（或 `Verify`），再读取已验证对象的
`Attributes`。

```go
err := netsec.VerifyAttributeCertificate(
    acDER, holderCert, authorityCert, time.Now(),
)
```

核验内容：

- 属性权威证书公钥对 `AttributeCertificateInfo` 的签名；
- Holder 中每个出现的约束：`baseCertificateID` 的签发者与序列号、
  `entityName` 以及对象摘要；
- 半开有效期 `[notBefore, notAfter)`；
- 拒绝未知 critical 扩展，忽略未知非 critical 扩展。

授权服务用属性证书声明角色，攻击者可把他人的属性证书搬到自己的公钥证书上。为Go授权包实现RFC 5755 Holder核验，供角色判定调用：属性权威签名有效，Holder的baseCertificateID须匹配出示证书的签发者与序列号，entityName或对象摘要出现时也须一致；有效期半开区间，未知critical扩展拒绝。用搬运、序列号相同签发者不同和合法持有者验证。
