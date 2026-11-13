# 分块编码语法与裸LF拒绝

前后两层对分块语法的宽容度不同，攻击者借裸 LF、带 0x 的块长或扩展里的换行走私第二个请求。为Go反向代理写可导入的 chunked 解析包：块长只接受十六进制数字，不得带 +、0x 或空白；行尾必须是 CRLF；扩展值若是引号串则不得含换行；同时有 Content-Length 与 Transfer-Encoding 的请求拒绝。用 "1;x=\"a\nb\""、裸 LF、0x10 和合法分块验证。

## 使用

```go
import (
	"io"
	"net/http"

	"example.org/netsec/q0637/chunked"
)

func proxyBody(r io.Reader) ([]byte, error) {
	// Decode 只返回已解码的正文，并拒绝任何不合规的分块语法。
	return chunked.Decode(r)
}

func acceptRequest(req *http.Request) error {
	// 同时出现 Content-Length 与 Transfer-Encoding 时返回
	// ErrConflictingFraming。
	return chunked.ValidateRequest(req)
}
```

需要读取 trailer 或流式处理时使用 `chunked.NewReader`；完整解码并保留 trailer
时使用 `chunked.DecodeWithTrailers`。

## 严格规则

- 块长只能是 `1*HEXDIG`，不接受 `+`、`-`、`0x`、前导/尾随空白或其他字符。
- chunk-size 行、块数据终止符、trailer 行和空 trailer 终止行都必须以 `CRLF` 结束。
- chunk extension 按 RFC 9110 的 token / quoted-string 语法校验；quoted-string
  中出现原始或转义的 CR/LF 都会拒绝。
- 请求头中只要同时存在 `Content-Length` 与 `Transfer-Encoding` 即拒绝，即使值为空。
