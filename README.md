# 间接CRL需要证书颁发者

间接CRL没有certificateIssuer扩展，条目被算到上一个颁发者头上。间接CRL的每一段都必须能确定颁发者；确定不了则这份CRL不能用。每段都有颁发者的间接CRL可以查询。可导入的Go库供证书路径调用。删掉中间一段的颁发者扩展。

## 用法

```go
crl, err := netsec.ParseCRL(der) // 也接受 *x509.RevocationList
if err != nil {
    return err // 间接CRL中任一条目无法确定颁发者时，整份 CRL 不可用
}

revoked, err := crl.IsRevoked(cert)
if err != nil {
    return err
}
_ = revoked
```

间接 CRL 中，每个条目必须带有 `certificateIssuer` 扩展，且扩展内必须包含
`directoryName`。不会从前一条目继承颁发者，也不会把缺少扩展的条目默认为
CRL 颁发者。查询同时匹配序列号和证书颁发者，因此不同 CA 下相同序列号的
证书不会混淆。
