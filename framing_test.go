package netsec

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestChunkedByteAttribution：chunked 正文精确解块，尾部之后的响应不被吞掉。
func TestChunkedByteAttribution(t *testing.T) {
	script := []step{{data: strings.Join([]string{
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n",
		"4\r\nWiki\r\n5\r\npedia\r\n0\r\nETag: x\r\n\r\n",
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nhi",
	}, "")}}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("chunked response: %v", err)
	}
	if resp.Framing != FramingChunked {
		t.Fatalf("framing = %s", resp.Framing)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("chunked body: %v", err)
	}
	if string(body) != "Wikipedia" {
		t.Fatalf("chunked body = %q, want Wikipedia", body)
	}
	if !r.Reusable() {
		t.Fatal("complete chunked response must keep connection reusable")
	}

	next, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("next response after chunked: %v", err)
	}
	body2, _ := io.ReadAll(next.Body)
	if string(body2) != "hi" {
		t.Fatalf("next body = %q, want hi", body2)
	}
}

// TestChunkedTruncation：最后一块未完成就断开，判截断并禁止复用。
func TestChunkedTruncation(t *testing.T) {
	script := []step{
		{data: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nWiki\r\n5\r\nabc"},
		{wait: 20 * time.Millisecond, close: true},
	}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))

	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if string(body) != "Wikiabc" {
		t.Fatalf("partial chunked body = %q, want Wikiabc", body)
	}
	if r.Reusable() {
		t.Fatal("truncated chunked body must forbid reuse")
	}
}

// TestInMemoryBoundaryNoOverRead 用字节流精确验证读取不会越过响应边界，
// 并覆盖 CL+TE 走私头拒绝。
func TestInMemoryBoundaryNoOverRead(t *testing.T) {
	stream := strings.Join([]string{
		"HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\nabc",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\n\r\nhello",
	}, "")
	r := NewReader(strings.NewReader(stream))

	first, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("first response: %v", err)
	}
	body, _ := io.ReadAll(first.Body)
	if string(body) != "abc" {
		t.Fatalf("first body = %q, want abc", body)
	}

	_, err = r.ReadResponse("GET")
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("CL+TE smuggling: err = %v, want ErrProtocol", err)
	}
	if r.Reusable() {
		t.Fatal("protocol error must forbid reuse")
	}
}

// TestHEADWithoutContentLength：HEAD 无长度字段时也不得进入关闭定界、吞掉后续响应。
func TestHEADWithoutContentLength(t *testing.T) {
	stream := "HTTP/1.1 200 OK\r\nServer: x\r\n\r\nHTTP/1.1 204 No Content\r\n\r\n"
	r := NewReader(strings.NewReader(stream))

	resp, err := r.ReadResponse("HEAD")
	if err != nil {
		t.Fatalf("HEAD response: %v", err)
	}
	if resp.Framing != FramingNone {
		t.Fatalf("HEAD framing = %s, want none", resp.Framing)
	}
	if !r.Reusable() {
		t.Fatal("HTTP/1.1 HEAD without length fields must stay reusable; only framing-close bodies force shutdown")
	}
	next, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("next response: %v", err)
	}
	if next.StatusCode != 204 {
		t.Fatalf("next response code = %d, want 204", next.StatusCode)
	}
}

// TestHTTP10HEADDefaultsToConnectionClose：HTTP/1.0 默认短连接，HEAD 虽无正文也不得复用。
func TestHTTP10HEADDefaultsToConnectionClose(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.0 200 OK\r\n\r\n"))
	resp, err := r.ReadResponse("HEAD")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Framing != FramingNone {
		t.Fatalf("framing = %s", resp.Framing)
	}
	if r.Reusable() {
		t.Fatal("HTTP/1.0 without keep-alive must not be reusable")
	}
}

// TestBodyNotConsumedBlocksNext：未读完正文就读下一个响应必须报错，防止字节归属混乱。
func TestBodyNotConsumedBlocksNext(t *testing.T) {
	stream := "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhelloHTTP/1.1 200 OK\r\n\r\n"
	r := NewReader(strings.NewReader(stream))

	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("response: %v", err)
	}
	peek := make([]byte, 2)
	if _, err := io.ReadFull(resp.Body, peek); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if string(peek) != "he" {
		t.Fatalf("peek = %q", peek)
	}
	if _, err := r.ReadResponse("GET"); err == nil {
		t.Fatal("expected error when previous body is not fully consumed")
	}

	// 读完剩余字节后即可继续解析同一连接上的下一个响应。
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "llo" {
		t.Fatalf("rest = %q %v", rest, err)
	}
	next, err := r.ReadResponse("GET")
	if err != nil || next.StatusCode != 200 {
		t.Fatalf("next = %v %v", next, err)
	}
}

// TestHTTP10KeepAlive 与 TestConnectionClose 校验版本/Connection 头驱动的复用判定。
func TestConnectionReuseRules(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		method   string
		framing  Framing
		reusable bool
	}{
		{
			name:     "http10-keepalive-fixed-length",
			raw:      "HTTP/1.0 200 OK\r\nConnection: keep-alive\r\nContent-Length: 2\r\n\r\nhi",
			method:   "GET",
			framing:  FramingLength,
			reusable: true,
		},
		{
			name:     "http10-no-length-closes",
			raw:      "HTTP/1.0 200 OK\r\n\r\nabc",
			method:   "GET",
			framing:  FramingClose,
			reusable: false,
		},
		{
			name:     "http11-connection-close",
			raw:      "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 2\r\n\r\nhi",
			method:   "GET",
			framing:  FramingLength,
			reusable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tc.raw))
			resp, err := r.ReadResponse(tc.method)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if resp.Framing != tc.framing {
				t.Fatalf("framing = %s, want %s", resp.Framing, tc.framing)
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Fatalf("drain: %v", err)
			}
			if r.Reusable() != tc.reusable {
				t.Fatalf("reusable = %v, want %v", r.Reusable(), tc.reusable)
			}
		})
	}
}

// TestChunkedSmallReads 用 1 字节缓冲触发完整状态机，核对每个字节的归属。
func TestChunkedSmallReads(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhi\r\n3\r\nbye\r\n0\r\n\r\n" +
		"HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nz"
	r := NewReader(strings.NewReader(raw))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got []byte
	one := make([]byte, 1)
	for {
		n, err := resp.Body.Read(one)
		got = append(got, one[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read byte: %v", err)
		}
	}
	if string(got) != "hibye" {
		t.Fatalf("decoded = %q, want hibye", got)
	}

	next, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("next response: %v", err)
	}
	tail, _ := io.ReadAll(next.Body)
	if string(tail) != "z" {
		t.Fatalf("tail = %q, want z", tail)
	}
}

// TestTruncationAcrossDelayedArrival 确保分段到达时截断判定稳定、无超时依赖错误。
func TestTruncationAcrossDelayedArrival(t *testing.T) {
	script := []step{
		{data: "HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nabc"},
		{wait: 30 * time.Millisecond, data: "de", close: true},
	}
	dial := startScriptServer(t, script)
	r := NewReader(dialScript(t, dial))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if string(body) != "abcde" {
		t.Fatalf("body = %q, want abcde", body)
	}
}
