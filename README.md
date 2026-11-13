# OCSP 装订响应 nonce 校验

该包供 TLS 栈导入，用于判断一份 DER 编码的 OCSP 装订响应是否可以用于当前握手。核心规则：

- ClientHello 请求了 nonce 时，响应必须带回完全相同的 nonce。
- 响应 nonce 缺失或不一致时，调用方必须忽略该装订响应。
- nonce 一致后，`producedAt` 与 `thisUpdate`/`nextUpdate` 时间窗还必须有效；缺失 `nextUpdate` 时最多允许 24 小时。
- 未请求 nonce 时，不要求响应携带 nonce，但仍校验响应时间。

实现只依赖 Go 标准库，不验证 OCSP 签名或证书状态；TLS 栈仍需完成证书链和 OCSP 签名验证。

```go
import (
	"errors"
	"time"

	netsec "example.org/netsec/q0236"
)

func useStaple(requestNonce, ocspDER []byte) error {
	return netsec.ValidateStapledResponse(ocspDER, requestNonce, time.Now())
}
```

需要区分拒绝原因时使用哨兵错误：

```go
err := netsec.ValidateStapledResponse(ocspDER, requestNonce, time.Now())
switch {
case errors.Is(err, netsec.ErrNonceMissing):
	// 请求了 nonce，但响应未带回。
case errors.Is(err, netsec.ErrNonceMismatch):
	// 响应 nonce 与请求不一致。
case errors.Is(err, netsec.ErrResponseExpired),
	errors.Is(err, netsec.ErrResponseNotYetValid):
	// 响应时间无效。
}
```

`ParseOCSPResponse` / `ExtractOCSPNonce` 可用于检查响应内容。若需要构造“nonce 不一致”的负例，可翻转装订响应副本中的首个 nonce 位：

```go
tampered, err := netsec.FlipOCSPResponseNonce(ocspDER)
if err != nil {
	return err
}
// tampered 的签名已失效，仅用于测试校验逻辑。
_ = tampered
```
