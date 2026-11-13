# 证明 JWT 的 c_nonce 校验

`example.org/netsec/q0120` 提供可导入的 Go 包，供凭证发行服务校验 OID4VCI
凭证证明：

- `aud` 必须严格等于凭证端点；数组形式的 `aud` 中每个值都必须等于该端点。
- `nonce` 必须等于发行方当前签发的 `c_nonce`。
- 校验签名、时间和 `aud` 通过后，`nonce` 才被原子消费，因此同一 `nonce`
  只能成功一次；并发重放同样只有一个请求成功。
- 错误 `aud`、错误 `nonce`、无效签名不会消费当前合法 `c_nonce`。
- 支持可信公钥、`kid` 回调、证明头内嵌公钥 JWK，以及
  HS/RS/PS/ES/EdDSA 系列算法。

## 接入示例

```go
package issuer

import (
	"net/http"
	"time"

	netsec "example.org/netsec/q0120"
)

var proofVerifier, _ = netsec.NewVerifier(
	"https://issuer.example.com/credential",
	// 可信公钥也可以是 PEM、证书、JWK，或改用 WithKeyFunc。
	netsec.WithVerificationKey(issuerTrustedPublicKey),
	netsec.WithMaxProofAge(5*time.Minute),
)

func credential(w http.ResponseWriter, r *http.Request) {
	proof := r.Header.Get("OpenID4VCI-Proof")
	if err := proofVerifier.ValidateProof(proof); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 校验通过后再签发凭证。
}

func credentialError(w http.ResponseWriter) {
	nonce, _ := proofVerifier.IssueNonce()
	// 将 nonce 放入 OID4VCI credential response 的 c_nonce 字段。
	_ = nonce
}
```

`IssueNonce` 返回新的当前 `c_nonce`。再次签发会让旧 nonce 失效。

多实例部署应实现 `netsec.NonceStore`，并保证 `Consume(scope, nonce)` 在数据库
中以单条条件更新同时完成“等于当前值”检查和“标记已使用”。

## 错误分类

使用 `errors.Is` 区分 `ErrAudienceMismatch`、`ErrNonceMismatch`、
`ErrNonceReused`、`ErrInvalidSignature` 等错误。只有
`ErrNonceReused` 表示一个已经成功消费过的 nonce 被再次使用。

## 测试

```sh
go test ./...
go vet ./...
```
