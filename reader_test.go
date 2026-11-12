package netsec

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestHEADResponseByteAttribution：HEAD 响应绝不能把同连接后续响应当正文。
func TestHEADResponseByteAttribution(t *testing.T) {
	headResp := "HTTP/1.1 200 OK\r\nContent-Length: 11\r\nContent-Type: text/plain\r\n\r\n"
	nextResp := "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"
	dial := startScriptServer(t, []step{{data: headResp + nextResp}})

	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("HEAD")
	if err != nil {
		t.Fatalf("read HEAD response: %v", err)
	}
	if resp.Framing != FramingNone {
		t.Fatalf("HEAD framing = %s, want none", resp.Framing)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil || len(got) != 0 {
		t.Fatalf("HEAD body = %q, %v; want empty, nil", got, err)
	}
	if !r.Reusable() {
		t.Fatal("connection must remain reusable after HEAD")
	}

	// 后续响应必须被完整解析，且正文恰好是 5 个字节，一个不多一个不少。
	resp2, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read pipelined response: %v", err)
	}
	if resp2.Framing != FramingLength || resp2.ContentLength != 5 {
		t.Fatalf("second framing = %s len=%d", resp2.Framing, resp2.ContentLength)
	}
	body, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read second body: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("second body = %q, want hello", body)
	}

	// 所有字节各归其主，再读只能得到干净 EOF。
	if _, err := r.ReadResponse("GET"); !errors.Is(err, io.EOF) {
		t.Fatalf("next read = %v, want io.EOF", err)
	}
}

// TestInformationalResponses：1xx 序列之后才是最终响应，1xx 不吞字节、不改复用性。
func TestInformationalResponses(t *testing.T) {
	script := []step{{data: strings.Join([]string{
		"HTTP/1.1 100 Continue\r\n\r\n",
		"HTTP/1.1 103 Early Hints\r\nLink: </style.css>; rel=preload\r\n\r\n",
		"HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nbody",
		"HTTP/1.1 204 No Content\r\nContent-Length: 999\r\n\r\n",
	}, "")}}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	for i, want := range []int{100, 103} {
		resp, err := r.ReadResponse("POST")
		if err != nil {
			t.Fatalf("informational #%d: %v", i, err)
		}
		if resp.StatusCode != want || resp.Framing != FramingNone {
			t.Fatalf("info #%d = code %d framing %s", i, resp.StatusCode, resp.Framing)
		}
		if n, err := io.ReadAll(resp.Body); err != nil || len(n) != 0 {
			t.Fatalf("info body must be empty, got %q %v", n, err)
		}
		if !r.Reusable() {
			t.Fatal("1xx must not change reusability")
		}
	}

	final, err := r.ReadResponse("POST")
	if err != nil {
		t.Fatalf("final response: %v", err)
	}
	if final.StatusCode != 200 || final.Framing != FramingLength {
		t.Fatalf("final = %d %s", final.StatusCode, final.Framing)
	}
	body, err := io.ReadAll(final.Body)
	if err != nil || string(body) != "body" {
		t.Fatalf("final body = %q %v", body, err)
	}

	// 204 即使携带 Content-Length 也没有正文；后续字节必须保留。
	noContent, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("204 response: %v", err)
	}
	if noContent.StatusCode != 204 || noContent.Framing != FramingNone {
		t.Fatalf("204 framing = %s", noContent.Framing)
	}
	if n, _ := io.ReadAll(noContent.Body); len(n) != 0 {
		t.Fatalf("204 body = %q, want empty", n)
	}
	if _, err := r.ReadResponse("GET"); !errors.Is(err, io.EOF) {
		t.Fatalf("after 204 = %v, want EOF", err)
	}
}

// TestTunnelSuccess：CONNECT 2xx 后连接交给隧道，原始字节一字节不漏地透传。
func TestTunnelSuccess(t *testing.T) {
	script := []step{{data: strings.Join([]string{
		"HTTP/1.1 200 Connection Established\r\n\r\n",
		"\x16\x03\x01tunnel-bytes",
	}, "")}}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("CONNECT")
	if err != nil {
		t.Fatalf("connect response: %v", err)
	}
	if resp.StatusCode != 200 || resp.Framing != FramingTunnel {
		t.Fatalf("connect framing = %d %s", resp.StatusCode, resp.Framing)
	}
	if r.Reusable() {
		t.Fatal("connection must not be reusable after tunnel switch")
	}

	tunnelWant := []byte("\x16\x03\x01tunnel-bytes")
	got := make([]byte, len(tunnelWant))
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("tunnel read: %v", err)
	}
	if !bytes.Equal(got, tunnelWant) {
		t.Fatalf("tunnel bytes = %v, want %v", got, tunnelWant)
	}

	// 隧道之后禁止再以 HTTP 响应序列方式读取。
	if _, err := r.ReadResponse("GET"); err == nil {
		t.Fatal("expected error when reading HTTP response on tunneled connection")
	}
}

// TestFixedLengthTruncation：定长正文提前关闭即截断，并禁止复用连接。
func TestFixedLengthTruncation(t *testing.T) {
	// 声明 10 字节只给 4 字节然后关闭，分段到达确保逐次 Read 也能判定。
	script := []step{
		{data: "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n"},
		{wait: 20 * time.Millisecond, data: "abcd", close: true},
	}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	if resp.Framing != FramingLength || resp.ContentLength != 10 {
		t.Fatalf("framing = %s len=%d", resp.Framing, resp.ContentLength)
	}
	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if string(body) != "abcd" {
		t.Fatalf("partial body = %q, want abcd", body)
	}
	if r.Reusable() {
		t.Fatal("truncated fixed-length body must forbid reuse")
	}
}

// TestCloseDelimitedBody：无长度字段时正文持续到关闭，且关闭后不可复用。
func TestCloseDelimitedBody(t *testing.T) {
	script := []step{
		{data: "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\n"},
		{wait: 20 * time.Millisecond, data: "part1-"},
		{wait: 20 * time.Millisecond, data: "part2", close: true},
	}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	if resp.Framing != FramingClose {
		t.Fatalf("framing = %s, want close-delimited", resp.Framing)
	}
	if r.Reusable() {
		t.Fatal("close-delimited response must mark connection non-reusable up front")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("close body: %v", err)
	}
	if string(body) != "part1-part2" {
		t.Fatalf("close body = %q", body)
	}
}
