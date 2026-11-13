# OCSP 冲突状态

同一证书的两份 SingleResponse 一个 good、一个 revoked 时，状态冲突，解析和校验都会失败，不会挑选 good。响应中只有 revoked（或重复条目全是 revoked）时按吊销处理。

本模块是可导入的纯 Go 库，证书路径校验可直接调用：

```go
import (
	"crypto/x509"

	netsec "example.org/netsec/q0279"
)

func check(cert, issuer *x509.Certificate, ocspDER []byte) error {
	// 完整校验签名、有效期和状态；good 返回 nil，revoked 返回
	// netsec.ErrCertificateRevoked（通过 errors.Is 判断）。
	return netsec.ValidateCertificateStatus(cert, issuer, ocspDER)
}
```

主要 API：

- `ParseBasicResponse` / `ParseResponse` / `ParseResponseForCert`：只解析和选取状态，不校验签名或时效。
- `VerifyResponse` / `VerifyResponseForCert`：校验签名、responder 授权和时效，再返回状态。
- `CheckStatus` / `CheckCertificateStatus`：完整校验后返回 `Good`、`Revoked` 或 `Unknown`。
- `ValidateResponse` / `ValidateCertificateStatus`：fail-closed 封装，`Revoked` 返回 `netsec.ErrCertificateRevoked`，`Unknown` 返回 `netsec.ErrUnknownStatus`。

冲突错误可通过 `errors.Is(err, netsec.ErrConflictingStatus)` 判断。`Response.Responses` 保留同一证书的全部匹配条目，便于审计。
