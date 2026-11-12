package netsec

import (
	"bytes"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func isPermissionDenied(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

// TestServeUDPRoundTrip 验证 UDP 请求处理路径：合法请求收到时间响应，
// 重放与认证失败的请求静默丢弃，绝不回包。
func TestServeUDPRoundTrip(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ResolveUDPAddr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		if isPermissionDenied(err) {
			t.Skipf("sandbox forbids udp bind: %v", err)
		}
		t.Fatalf("ListenUDP: %v", err)
	}
	defer conn.Close()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeUDP(conn) }()

	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	request, _, _ := sealClientRequest(t, srv, secrets, randomUID(t, 32))

	client, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer client.Close()

	if _, err := client.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response := make([]byte, maxUDPPacket)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, err := client.Read(response)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	echoedUID, newCookie, err := openClientResponse(t, secrets, response[:n])
	if err != nil {
		t.Fatalf("verify response: %v", err)
	}
	if !bytes.Equal(trimZeroPadding(echoedUID), request[ntpHeaderSize+extHeaderSize:ntpHeaderSize+extHeaderSize+32]) {
		t.Fatal("response did not echo the unique identifier verbatim")
	}
	if len(newCookie) == 0 {
		t.Fatal("response missing fresh cookie")
	}

	// 重放同一报文：服务器必须静默，客户端读超时即证明“不回复时间”。
	if _, err := client.Write(request); err != nil {
		t.Fatalf("write replay: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := client.Read(response); err == nil {
		t.Fatal("server replied to a replayed request")
	}

	// 篡改后的请求同样静默。
	tampered := append([]byte(nil), request...)
	tampered[ntpHeaderSize+extHeaderSize] ^= 0xFF
	if _, err := client.Write(tampered); err != nil {
		t.Fatalf("write tampered request: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := client.Read(response); err == nil {
		t.Fatal("server replied to an authentication failure")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("ServeUDP returned error: %v", err)
	}
}
