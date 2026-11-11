# ocspcheck

TLS客户端信任服务端装订的OCSP响应，伪造或过期的响应会让已吊销证书继续通过。给Go客户端实现可挂到tls.Config.VerifyConnection的OCSP校验组件：确认响应对应叶子证书的序列号和签发者哈希，签名者必须是签发CA或带OCSPSigning用途的委派证书，按thisUpdate、nextUpdate和允许偏差判断时效，unknown与缺失响应按调用方策略处理。用自建CA构造委派越权、时间越界和序列号不符的响应测试。

## 用法

```go
import "example.org/ocspcheck"

checker := &ocspcheck.Config{
    Skew:          time.Minute,       // 允许的时钟偏差，零值默认 1 分钟
    MaxAge:        7 * 24 * time.Hour, // 无 nextUpdate 时的最大可接受年龄；负值表示强制要求 nextUpdate
    UnknownPolicy: ocspcheck.UnknownFails,  // unknown 状态默认拒绝
    MissingPolicy: ocspcheck.MissingFails,  // 缺少装订默认拒绝
}

tlsConfig := &tls.Config{
    VerifyConnection: checker.VerifyConnection,
}
```

`crypto/tls` 自带的证书链校验仍然先执行，本回调在其之上额外强制 OCSP 装订校验。

## 校验规则（对应 RFC 6960）

- **CertID 匹配**：SingleResponse 的序列号必须等于叶子证书序列号，issuerNameHash 与 issuerKeyHash 必须与签发 CA 匹配（支持 SHA-1/256/384/512）。
- **签名者授权**：
  - 签发 CA 直接签名（ResponderID 为 byName 或 byKey 均支持）；或
  - 委派响应者证书由签发 CA 直接签发、当前时间在有效期内、`IsCA=false`、含 digitalSignature key usage 且带 `id-kp-OCSPSigning` 扩展用途，否则拒绝。
- **时效**：`now+Skew < thisUpdate` 拒绝；带 `nextUpdate` 时 `now-Skew > nextUpdate` 拒绝；不带 `nextUpdate` 时按 `MaxAge` 限制年龄。
- **状态策略**：good 放行；revoked 返回 `ErrRevoked`；unknown 与缺失装订分别由 `UnknownPolicy`、`MissingPolicy` 决定，默认全部 fail-closed。

可通过 `errors.Is` 识别 `ErrRevoked`、`ErrUnknownStatus`、`ErrMissingResponse`。

## 测试

`go test ./...` 使用自建 ECDSA/RSA CA 构造装订响应，覆盖：

- 委派越权：缺少 OCSPSigning EKU、委派证书本身是 CA、未内嵌委派证书、签名被篡改；
- 时间越界：thisUpdate 未来、nextUpdate 过期（含偏差边界）、缺失 nextUpdate 的 MaxAge 策略；
- 序列号/哈希不符：错误序列号、另一张合法证书的序列号、错误 issuer name/key hash；
- 状态与缺失策略：revoked、unknown 放行/拒绝、无装订放行/拒绝；
- 真实 TLS 握手（内存 `net.Pipe`）验证可挂到 `tls.Config.VerifyConnection`，以及 RSA PKCS#1v1.5 与 RSA-PSS 签名。
