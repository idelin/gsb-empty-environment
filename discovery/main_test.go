package discovery

import (
	"os"
	"testing"
)

// 测试环境使用进程内内存网络承载真实 HTTP 协议栈，无需占用端口。
func TestMain(m *testing.M) {
	restore := UseInMemoryTransport()
	code := m.Run()
	restore()
	os.Exit(code)
}
