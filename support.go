package netsec

import (
	"errors"
	"io"
	"net/http"
)

// ErrNonReplayableBody 表示请求带了不可重放的 Body，却因 401/407
// 需要重新发送。调用方应改用实现 GetBody 的请求（如 bytes.Reader）。
var ErrNonReplayableBody = errors.New("netsec: request body cannot be replayed after authentication challenge")

// drainClose 排空并关闭挑战响应体，使底层 TCP 连接可以安全回到连接池。
func drainClose(resp *http.Response) {
	if resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
