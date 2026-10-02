package controller

import (
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"leasefed/witness"
)

// TestHTTPTransport 验证 HTTPClient 与法官 HTTP 服务的端到端交互。
// 在禁止监听端口的沙箱环境中自动跳过。
func TestHTTPTransport(t *testing.T) {
	if l, err := net.Listen("tcp4", "127.0.0.1:0"); err != nil {
		t.Skipf("当前环境禁止监听端口,跳过 HTTP 传输测试: %v", err)
	} else {
		l.Close()
	}

	core := witness.NewCore()
	srv := httptest.NewServer(core.Handler())
	defer srv.Close()

	c := &HTTPClient{Base: srv.URL}

	lease, ok, err := c.Acquire("dc1", 200*time.Millisecond)
	if err != nil || !ok || lease.Epoch != 1 {
		t.Fatalf("HTTP 申请租约失败: %+v ok=%v err=%v", lease, ok, err)
	}
	// 他人申请应被拒
	if _, ok, err := c.Acquire("dc2", 200*time.Millisecond); err != nil || ok {
		t.Fatalf("租约有效期内他人申请应被拒: ok=%v err=%v", ok, err)
	}
	// 合法续租
	renewed, ok, err := c.Renew("dc1", lease.Epoch, 200*time.Millisecond)
	if err != nil || !ok || renewed.Epoch != lease.Epoch {
		t.Fatalf("HTTP 续租失败: %+v ok=%v err=%v", renewed, ok, err)
	}
	// 失效凭证续租应被拒
	if _, ok, err := c.Renew("dc1", lease.Epoch+1, 200*time.Millisecond); err != nil || ok {
		t.Fatalf("失效凭证续租应被拒: ok=%v err=%v", ok, err)
	}
}
