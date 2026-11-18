# DANE TLSA选择器匹配

邮件投递代理靠TLSA绑定服务器证书，选择器或匹配类型错位会让错误证书与记录相等。为Go投递组件实现DANE-EE与DANE-TA校验包，供TLS握手后调用（TLSA已通过DNSSEC验证）：selector 0比较整证书、1比较SPKI，matching type 0、1、2分别比较原文、SHA-256、SHA-512；DANE-EE不查名称与有效期，DANE-TA要求链上有匹配证书并只接受能链到该TA的路径；同一名字多条可用TLSA记录时任一命中即通过，全部不命中才拒绝；selector与matching错位必须拒绝而不能退化成字节比较。用异选择器同字节、TA旁路、多记录部分命中和合法证书验证。

## 用法

TLSA 记录需先由调用方完成 DNSSEC 验证，再在 TLS 握手后调用：

```go
records := []netsec.TLSARecord{{
    Usage:        netsec.UsageDANEEE,            // 3 = DANE-EE，2 = DANE-TA
    Selector:     netsec.SelectorCert,           // 0 = 整证书，1 = SPKI
    MatchingType: netsec.MatchingSHA256,         // 0 = 原文，1/2 = SHA-256/512
    Data:         associationData,
}}

if err := netsec.VerifyConnection(conn.ConnectionState(), records); err != nil {
    // err 为 ErrNoMatch（全部可用记录均不命中）或 ErrNoTLSARecords（无可用记录）
}
```

## 语义

- selector 先决定取 `Raw`（整证书）还是 `RawSubjectPublicKeyInfo`（SPKI），
  matching type 再决定原文或定长摘要比较；未知字段、摘要长度不符的记录直接忽略，
  不会退化为裸字节比较。
- DANE-EE 只匹配叶证书，不做 DNS 名称与有效期检查（过期、自签叶证书均可通过）。
- DANE-TA 只在链上的 CA 证书中找锚点，并以该锚点作为唯一根重建验证路径，
  防止“锚点出现在 bundle 中但叶证书并不链向它”的旁路攻击；有效期仍按 PKIX 校验。
- 多条记录时任一命中即通过；全部可用记录不命中才返回 `ErrNoMatch`。
