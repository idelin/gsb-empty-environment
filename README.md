# 证书签名方案回退

一个小型、可导入的 Go 库，供 TLS 栈正确处理
`signature_algorithms` 与 `signature_algorithms_cert` 两个扩展。

## 规则

- `signature_algorithms_cert` 存在时，证书链只按该扩展中的方案过滤。
- `signature_algorithms_cert` 缺失时，才回退到 `signature_algorithms`。
- 选择结果必须同时命中“适用列表”和本地支持列表；两个列表都不允许的方案不会被选中。
- 证书方案只要命中适用列表就接受，不另行假设客户端偏好。
- “扩展缺失”与“扩展存在但列表为空”是不同的状态，分别表示回退和不允许任何证书方案。

## 用法

```go
import netsec "example.org/netsec/q0251"

// signatureAlgorithms 来自 signature_algorithms。
// certAlgorithms 为 nil 且 present=false 表示扩展缺失。
scheme, ok := netsec.SelectCertificateSignatureScheme(
	localSchemes,
	signatureAlgorithms,
	certAlgorithms,
	certExtensionPresent,
)
if !ok {
	// 没有双方都允许的证书签名方案。
}
```

也可以先构造策略对象：

```go
policy := netsec.NewCertificateSignatureAlgorithms(
	signatureAlgorithms,
	certAlgorithms,
	certExtensionPresent,
)

applicable := policy.ApplicableCertificateSchemes()
allowed := policy.AcceptsCertificateScheme(netsec.PSSWithSHA256)
selected, ok := policy.SelectCertificateScheme(localSchemes)
```

如果 TLS 栈需要自己保留错误语义：

```go
scheme, err := netsec.NegotiateCertificateSignatureScheme(
	localSchemes,
	signatureAlgorithms,
	certAlgorithms,
	certExtensionPresent,
)
if errors.Is(err, netsec.ErrNoSuitableCertificateSignatureScheme) {
	// 握手失败。
}
```

## 弱方案示例

`PKCS1WithSHA1`（`rsa_pkcs1_sha1`，`0x0201`）是一个故意保留的弱方案。若它只出现在
`signature_algorithms` 中：

- `signature_algorithms_cert` 存在时，它不能用于证书签名；
- `signature_algorithms_cert` 缺失时，它随 `signature_algorithms` 一起作为回退列表生效。

测试 `TestSchemeOnlyInHandshakeListRejectedForCertificate` 和
`TestFallbackToSignatureAlgorithmsWhenCertExtensionAbsent` 覆盖了这两个分支。

## x509 证书链

```go
ok := netsec.CertificateChainAllowed(
	chain,
	signatureAlgorithms,
	certAlgorithms,
	certExtensionPresent,
)

chains := netsec.FilterCertificateChains(
	candidateChains,
	signatureAlgorithms,
	certAlgorithms,
	certExtensionPresent,
)
```

`CertificateChainAllowed` 要求链上每个证书的签名方案都命中适用列表；
`FilterCertificateChain` 和 `FilterCertificateChains` 返回通过过滤的证书或候选链。

## 主要 API

- `ApplicableCertificateSchemes` / `EffectiveCertificateSignatureSchemes`
- `AcceptsCertificateSignatureScheme` / `CertificateSignatureSchemeAllowed`
- `SelectCertificateSignatureScheme` / `SelectCertificateScheme`
- `NegotiateCertificateSignatureScheme`
- `FilterCertificateSignatureSchemes`
- `CertificateChainAllowed` / `FilterCertificateChain(s)`
- `ParseSignatureSchemeList` / `MarshalSignatureSchemeList`
