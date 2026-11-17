# 签名属性必需集校验

SignedData里，攻击者可追加未签名内容，或让缺少message-digest的签名属性被接受。为Go文件验签包实现RFC 5652签名者校验，供归档导入调用：signedAttrs存在时须含content-type与message-digest各一次，验签输入把隐式标签A0换成SET标签31后重新DER编码，content-type须与封装内容类型一致。用缺属性、重复属性和改摘要验证。

## 使用

```go
// der 可以是完整 CMS ContentInfo，也可以是 PEM(PKCS7/CMS)。
// 第三个参数可传 *x509.CertPool、[]*x509.Certificate 或 *x509.Certificate。
err := netsec.VerifySignedData(der, content, roots)
```

`content` 为分离签名内容；若 SignedData 自带 eContent，则传入内容必须与其完全一致。
`VerifyFile` / `VerifyFiles` 可直接接收文件路径或已读取的 `[]byte`。

默认按 RFC 5652 允许不带 signedAttrs 的 SignerInfo；归档导入如需强制该策略，使用
`RequireSignedAttributes()`。未提供信任根时只做密码学验签；生产导入应传入
`WithRoots` 或 `WithTrustedCertificates` 以校验证书链。

## 校验规则

- signedAttrs 存在时，`content-type` 与 `message-digest` 必须各出现一次。
- 任一属性类型不得重复，必需属性值集合必须恰好包含一个值。
- `content-type` 必须等于 EncapsulatedContentInfo 的 `eContentType`。
- `message-digest` 必须等于使用签名者摘要算法计算出的内容摘要。
- 验签输入使用 signedAttrs 内容八位组重新编码为 SET（标签 `31`），而不是隐式 `A0` 标签。
- 顶层尾随数据、重复字段以及对封装内容的任何追加或替换都会被拒绝。
