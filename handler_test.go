package netsec

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandlerPreservesBufferedEarlyData(t *testing.T) {
	early := []byte("GET /early HTTP/1.1\r\n\r\n")
	clientConn := newMemoryConn()
	readWriter := bufio.NewReadWriter(
		bufio.NewReaderSize(strings.NewReader(string(early)), 4096),
		bufio.NewWriter(clientConn),
	)
	writer := &hijackResponseWriter{
		header:     make(http.Header),
		connection: clientConn,
		readWriter: readWriter,
	}

	target, targetPeer := net.Pipe()
	defer targetPeer.Close()
	targetCounter := &writeCountingConn{Conn: target}

	authStarted := make(chan struct{})
	authRelease := make(chan struct{})
	var once sync.Once
	ingress := NewIngress(
		WithAuthorizeFunc(func(context.Context, Request) error {
			once.Do(func() { close(authStarted) })
			<-authRelease
			return nil
		}),
		WithDialer(DialerFunc(func(context.Context, string, string) (net.Conn, error) {
			return targetCounter, nil
		})),
	)
	handler := &Handler{Ingress: ingress}
	request := &http.Request{
		Method: http.MethodConnect,
		Host:   "target.example:443",
		Header: make(http.Header),
	}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, request)
		close(done)
	}()

	waitClosed(t, authStarted, "authorization did not start")
	assertNoTargetBytes(t, targetPeer, "HTTP handler forwarded buffered early data before authorization")

	close(authRelease)
	if got := readN(t, targetPeer, len(early)); !bytes.Equal(got, early) {
		t.Fatalf("target early data = %q, want %q", got, early)
	}
	if !bytes.Contains(clientConn.Bytes(), []byte("200 Connection Established")) {
		t.Fatalf("buffered client response = %q", clientConn.Bytes())
	}

	_ = targetPeer.Close()
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Handler did not return")
	}
}

type hijackResponseWriter struct {
	header     http.Header
	connection net.Conn
	readWriter *bufio.ReadWriter
}

func (w *hijackResponseWriter) Header() http.Header {
	return w.header
}

func (w *hijackResponseWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (w *hijackResponseWriter) WriteHeader(int) {}

func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.connection, w.readWriter, nil
}

type memoryConn struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	closed chan struct{}
	once   sync.Once
}

func newMemoryConn() *memoryConn {
	return &memoryConn{closed: make(chan struct{})}
}

func (c *memoryConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *memoryConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	return c.buffer.Write(p)
}

func (c *memoryConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *memoryConn) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buffer.Bytes()...)
}

func (c *memoryConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *memoryConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "memory" }
func (a testAddr) String() string  { return string(a) }
