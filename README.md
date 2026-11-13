# 证书机构扩展过滤

certificate_authorities列出的颁发者与实际证书链不符仍被接受。若扩展存在，末端证书的颁发者必须命中列表；不命中则这张证书不能使用。颁发者命中列表的证书继续路径验证。可导入的Go库供TLS栈调用。换成列表外的颁发者。

## 行为

- 扩展不存在（`nil`）：不做颁发者过滤，照常进行 X.509 路径验证。
- 扩展存在且列表非空：末端证书的 `RawIssuer` 必须与某个 DER 编码的 X.501 Name 逐字节相等。
- 扩展存在但列表为空：任何末端证书都不可用。
- 颁发者不命中：立即返回 `ErrIssuerNotAuthorized`，不会回退到其他证书，也不会继续路径验证。
- 颁发者命中：继续调用 `x509.Certificate.Verify` 做正常的信任链、有效期、用途和主机名校验。
- 扩展格式错误或已发送但内容未捕获：失败关闭，返回 `ErrMalformedCertificateAuthorities` 或 `ErrAuthoritiesUnavailable`。

匹配只针对末端证书的颁发者，不会因为链中某个中间证书命中列表就放行末端证书。

## 解析与过滤

```go
payload, err := netsec.MarshalCertificateAuthorities(netsec.Authorities{
    issuer.RawSubject,
})
if err != nil {
    return err
}

authorities, err := netsec.ParseCertificateAuthorities(payload)
if err != nil {
    return err
}
if err := netsec.ValidateCertificateAuthorities(leaf, authorities); err != nil {
    return err // 列表外颁发者 -> ErrIssuerNotAuthorized
}
```

从 ClientHello 或 TLS 1.3 `CertificateRequest` 中提取：

```go
hello, err := netsec.ParseClientHello(clientHelloBytes)
if err != nil {
    return err
}
extension := hello.Authorities // Extension{Present, Authorities}

requestExtension, err := netsec.ParseCertificateRequestCertificateAuthorities(certificateRequestBytes)
```

## 路径验证

```go
chains, err := netsec.VerifyChain(chain, authorities, x509.VerifyOptions{
    Roots:     roots,
    DNSName:   "server.example",
    KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
})
if err != nil {
    return err
}
_ = chains
```

## crypto/tls 集成

服务端必须使用 `NewListener` 或 `NewServerConn`，因为 `crypto/tls` 不会把
`certificate_authorities` 的内容暴露给 `GetCertificate`。本库通过包装连接捕获
ClientHello，并在选择证书时执行过滤。

自定义 TLS 栈也可以自行实现 `ClientHelloSource` 接口（`ClientHelloBytes() []byte`），
然后调用 `netsec.AuthoritiesFromClientHello` 解析扩展。

```go
listener, err := netsec.Listen("tcp", ":8443", &tls.Config{
    Certificates: []tls.Certificate{allowed, otherCA},
})
if err != nil {
    return err
}
defer listener.Close()
```

客户端 mTLS 使用 `WrapClientConfig`，确保服务端 `AcceptableCAs` 命中客户端末端
证书的颁发者，而不是链中任意一张证书：

```go
config := netsec.WrapClientConfig(&tls.Config{
    Certificates: []tls.Certificate{clientCertificate},
    RootCAs:      roots,
})
```

如果直接使用 `tls.Server` 而未包装连接，库会检测到扩展被声明但内容不可用，并以
`ErrAuthoritiesUnavailable` 失败关闭。

## 错误

- `ErrIssuerNotAuthorized`：末端证书颁发者不在列表内。
- `ErrNoUsableCertificate`：没有可用证书；与列表不匹配时会同时包装
  `ErrIssuerNotAuthorized`，`errors.Is` 对两者均返回真。
- `ErrMalformedCertificateAuthorities`：扩展编码非法。
- `ErrAuthoritiesUnavailable`：扩展存在但内容未捕获。
- `ErrMissingLeafCertificate`：缺少末端证书。
