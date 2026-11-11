package wsupgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

const testKey = "dGhlIHNhbXBsZSBub25jZQ=="

// fakeUpstream drives one raw TCP connection with custom behavior, so tests can
// emit malformed HTTP and post-response bytes that a real http.Server would
// refuse to send.
type fakeUpstream struct {
	t      *testing.T
	handle func(conn net.Conn, req *http.Request, reader *bufio.Reader)
	conn   net.Conn
}

func startFakeUpstream(t *testing.T, handle func(net.Conn, *http.Request, *bufio.Reader)) *fakeUpstream {
	t.Helper()
	return &fakeUpstream{t: t, handle: handle}
}

func (f *fakeUpstream) proxy() *Proxy {
	p := NewProxy(&url.URL{Scheme: "http", Host: "upstream.example"})
	p.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return f.conn, nil
	}
	return p
}

// newPipePair wires an in-memory full-duplex path:
// clientConn --[proxy]-- upConn (handled by the fake upstream). The proxy runs
// the real http.Server request machinery over its end of the pipe, so hijack,
// buffered bytes and CloseWrite all behave as over a real socket, without
// needing any loopback port (the sandbox forbids bind).
func newPipePair(t *testing.T, f *fakeUpstream, earlyData []byte) (net.Conn, *bufio.Reader) {
	t.Helper()
	clientConn, clientToProxy := halfClosePipe()
	upFromProxy, upConn := halfClosePipe()
	f.conn = upFromProxy
	t.Cleanup(func() {
		upConn.Close()
		upFromProxy.Close()
		clientToProxy.Close()
	})

	// Proxy side: drive one HTTP request over its pipe end.
	go func() {
		defer clientToProxy.Close()
		server := &http.Server{Handler: f.proxy()}
		server.Serve(newOneShotListener(clientToProxy))
	}()

	// Upstream side.
	go func() {
		reader := bufio.NewReader(upConn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			upConn.Close()
			return
		}
		f.handle(upConn, req, reader)
	}()

	t.Cleanup(func() { clientConn.Close() })
	clientConn.SetDeadline(time.Now().Add(5 * time.Second))

	request := "GET /chat HTTP/1.1\r\n" +
		"Host: upstream.example\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Key: " + testKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	go func() {
		io.WriteString(clientConn, request)
		if len(earlyData) > 0 {
			clientConn.Write(earlyData)
		}
	}()
	return clientConn, bufio.NewReader(clientConn)
}

// oneShotListener hands out a single, already-connected pipe and then returns
// ErrServerClosed so http.Server.Serve exits cleanly.
type oneShotListener struct {
	conn net.Conn
	done chan struct{}
}

func newOneShotListener(conn net.Conn) *oneShotListener {
	return &oneShotListener{conn: conn, done: make(chan struct{})}
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	if l.conn == nil {
		<-l.done
		return nil, http.ErrServerClosed
	}
	conn := l.conn
	l.conn = nil
	return conn, nil
}

func (l *oneShotListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *oneShotListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// halfClosePipe returns an in-memory, full-duplex connection pair that supports
// TCP-style half-close: CloseWrite makes the peer's Read return EOF while reads
// on the writing side remain usable. net.Pipe alone closes both directions.
func halfClosePipe() (net.Conn, net.Conn) {
	a := newPipeConn()
	b := newPipeConn()
	shared := &sync.Mutex{}
	a.mu, b.mu = shared, shared
	a.cond = sync.NewCond(shared)
	b.cond = sync.NewCond(shared)
	a.peer, b.peer = b, a
	return a, b
}

type pipeConn struct {
	peer          *pipeConn
	mu            *sync.Mutex
	cond          *sync.Cond
	buf           bytes.Buffer
	writeDone     bool
	closed        bool
	readDeadline  time.Time
	writeDeadline time.Time
}

func newPipeConn() *pipeConn {
	c := &pipeConn{}
	return c
}

var errPipeTimeout = timeoutError{}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func (c *pipeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	for {
		if c.buf.Len() > 0 {
			n, err := c.buf.Read(p)
			c.mu.Unlock()
			return n, err
		}
		if c.closed {
			c.mu.Unlock()
			return 0, net.ErrClosed
		}
		peerClosed := c.peer != nil && c.peer.closed
		peerWriteDone := c.peer != nil && c.peer.writeDone
		if peerClosed || peerWriteDone {
			c.mu.Unlock()
			return 0, io.EOF
		}
		if !c.readDeadline.IsZero() && !time.Now().Before(c.readDeadline) {
			c.mu.Unlock()
			return 0, errPipeTimeout
		}
		c.cond.Wait()
	}
}

func (c *pipeConn) Write(p []byte) (int, error) {
	peer := c.peer
	peer.mu.Lock()
	if !peer.writeDeadline.IsZero() && !time.Now().Before(peer.writeDeadline) {
		peer.mu.Unlock()
		return 0, errPipeTimeout
	}
	if peer.closed {
		peer.mu.Unlock()
		return 0, net.ErrClosed
	}
	if c.closed {
		peer.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	n, err := peer.buf.Write(p)
	peer.mu.Unlock()
	peer.cond.Broadcast()
	return n, err
}

func (c *pipeConn) CloseWrite() error {
	c.mu.Lock()
	c.writeDone = true
	c.mu.Unlock()
	c.cond.Broadcast()
	if c.peer != nil {
		c.peer.cond.Broadcast()
	}
	return nil
}

func (c *pipeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.writeDone = true
	c.mu.Unlock()
	c.cond.Broadcast()
	if c.peer != nil {
		c.peer.cond.Broadcast()
	}
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr{} }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr{} }

// armWatchdog wakes a blocked Read when the read deadline fires.
func (c *pipeConn) armWatchdog(deadline time.Time) {
	if deadline.IsZero() {
		return
	}
	d := time.Until(deadline)
	if d <= 0 {
		c.cond.Broadcast()
		return
	}
	time.AfterFunc(d, c.cond.Broadcast)
}

func (c *pipeConn) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.readDeadline = deadline
	c.mu.Unlock()
	c.armWatchdog(deadline)
	return nil
}

func (c *pipeConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *pipeConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func expectAccept() string {
	sum := sha1.Sum([]byte(testKey + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// readResp reads one HTTP response from the proxy and returns its body.
func readResp(t *testing.T, reader *bufio.Reader) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, body
}

func writeSwitchingProtocols(conn net.Conn, accept string) {
	io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n")
	io.WriteString(conn, "Upgrade: websocket\r\n")
	io.WriteString(conn, "Connection: Upgrade\r\n")
	io.WriteString(conn, "Sec-WebSocket-Accept: "+accept+"\r\n\r\n")
}

// TestUpstream200_NoTunnel: a non-101 upstream answer must be delivered as a
// normal HTTP response, the tunnel must never open, and the connection must be
// closed so pipelined smuggling bytes cannot be reinterpreted as a request.
func TestUpstream200_NoTunnel(t *testing.T) {
	smuggled := []byte("GET /admin HTTP/1.1\r\nHost: x\r\n\r\n")
	upstream := startFakeUpstream(t, func(conn net.Conn, req *http.Request, reader *bufio.Reader) {
		if req.Header.Get("Upgrade") != "websocket" {
			t.Errorf("upstream did not receive upgrade request")
		}
		body := []byte("plain response body")
		io.WriteString(conn, "HTTP/1.1 200 OK\r\n")
		io.WriteString(conn, "Content-Type: text/plain\r\n")
		io.WriteString(conn, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n")
		conn.Write(body)
		// Bytes that belong to no response: a smuggled request the proxy must
		// never relay to the client as a second response.
		conn.Write([]byte("JUNK-AFTER-BODY"))
		// Half-close the write side: a real server closes after a response on a
		// non-upgraded connection. The unframed JUNK above stays unconsumed and
		// must never reach the client.
		conn.Close()
	})

	conn, reader := newPipePair(t, upstream, smuggled)

	resp, body := readResp(t, reader)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "plain response body" {
		t.Fatalf("body = %q", body)
	}
	// Connection must be closed after the rejected upgrade.
	one := make([]byte, 1)
	if n, err := conn.Read(one); n != 0 || err == nil {
		t.Fatalf("expected EOF after plain response, got n=%d err=%v", n, err)
	}
}

// TestUpstream426_NoTunnel: 426 Upgrade Required is still an ordinary response.
func TestUpstream426_NoTunnel(t *testing.T) {
	upstream := startFakeUpstream(t, func(conn net.Conn, req *http.Request, reader *bufio.Reader) {
		io.WriteString(conn, "HTTP/1.1 426 Upgrade Required\r\n")
		io.WriteString(conn, "Content-Length: 13\r\n")
		io.WriteString(conn, "Upgrade: websocket\r\n")
		io.WriteString(conn, "Connection: Upgrade\r\n\r\n")
		conn.Write([]byte("upgrade first"))
		conn.Close()
	})

	conn, reader := newPipePair(t, upstream, nil)

	resp, body := readResp(t, reader)
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426", resp.StatusCode)
	}
	if string(body) != "upgrade first" {
		t.Fatalf("body = %q", body)
	}
	if headerHasToken(resp.Header, "Connection", "upgrade") {
		t.Fatalf("hop-by-hop Connection header must be stripped")
	}
	one := make([]byte, 1)
	if n, err := conn.Read(one); n != 0 || err == nil {
		t.Fatalf("expected connection close, got n=%d err=%v", n, err)
	}
}

// TestMalformed101_Rejected: every flavor of invalid 101 must produce a 502 and
// never a 101 switch on the client side.
func TestMalformed101_Rejected(t *testing.T) {
	cases := map[string]func(net.Conn){
		"wrong accept": func(conn net.Conn) {
			writeSwitchingProtocols(conn, "definitely-wrong")
		},
		"missing accept": func(conn net.Conn) {
			io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n")
			io.WriteString(conn, "Upgrade: websocket\r\n")
			io.WriteString(conn, "Connection: Upgrade\r\n\r\n")
		},
		"wrong upgrade protocol": func(conn net.Conn) {
			io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n")
			io.WriteString(conn, "Upgrade: h2c\r\n")
			io.WriteString(conn, "Connection: Upgrade\r\n")
			io.WriteString(conn, "Sec-WebSocket-Accept: "+expectAccept()+"\r\n\r\n")
		},
		"connection without upgrade token": func(conn net.Conn) {
			io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n")
			io.WriteString(conn, "Upgrade: websocket\r\n")
			io.WriteString(conn, "Connection: keep-alive\r\n")
			io.WriteString(conn, "Sec-WebSocket-Accept: "+expectAccept()+"\r\n\r\n")
		},
	}

	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			upstream := startFakeUpstream(t, func(conn net.Conn, req *http.Request, reader *bufio.Reader) {
				respond(conn)
				// A frame the proxy must not relay because the switch is invalid.
				conn.Write([]byte("TUNNEL-BYTES"))
				conn.Close()
			})
			conn, reader := newPipePair(t, upstream, []byte("early-client-bytes"))

			resp, _ := readResp(t, reader)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", resp.StatusCode)
			}
			// No tunnel bytes may reach the client; the socket closes.
			one := make([]byte, 1)
			if n, err := conn.Read(one); n != 0 || err == nil {
				t.Fatalf("expected EOF, got n=%d err=%v", n, err)
			}
		})
	}
}

// TestValidUpgrade_TunnelPreservesBufferedBytes verifies the happy path and the
// no-loss guarantee for bytes buffered on BOTH sides before the tunnel starts:
// the client sends early data before reading the 101, and the upstream emits a
// frame immediately after the 101. Ordering and content must be preserved.
func TestValidUpgrade_TunnelPreservesBufferedBytes(t *testing.T) {
	upstreamFrame := []byte("upstream-buffered-frame")
	upstream := startFakeUpstream(t, func(conn net.Conn, req *http.Request, reader *bufio.Reader) {
		writeSwitchingProtocols(conn, expectAccept())
		// Written before reading any client data: likely buffered inside the
		// proxy when it validates the response.
		conn.Write(upstreamFrame)

		// Read everything the client sends; the first bytes must be early data.
		got, err := io.ReadAll(reader)
		if err != nil && err != io.EOF && err != net.ErrClosed {
			t.Errorf("upstream read: %v", err)
			return
		}
		want := "early-client-frame" + "second-client-frame"
		if string(got) != want {
			t.Errorf("upstream received %q, want %q", got, want)
		}
	})

	conn, reader := newPipePair(t, upstream, []byte("early-client-frame"))

	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read 101: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != expectAccept() {
		t.Fatalf("bad accept header %q", resp.Header.Get("Sec-WebSocket-Accept"))
	}

	// Upstream's pre-tunnel frame must survive, in order.
	got := make([]byte, len(upstreamFrame))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if string(got) != string(upstreamFrame) {
		t.Fatalf("upstream frame = %q", got)
	}

	if _, err := conn.Write([]byte("second-client-frame")); err != nil {
		t.Fatalf("write second frame: %v", err)
	}
	// Give the echo loop time, then close so the tunnel winds down.
	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

// TestHalfClose: when the client half-closes its write side, the upstream must
// observe EOF while still being able to send; the reverse direction keeps
// flowing until the upstream also ends.
func TestHalfClose(t *testing.T) {
	upstream := startFakeUpstream(t, func(conn net.Conn, req *http.Request, reader *bufio.Reader) {
		writeSwitchingProtocols(conn, expectAccept())

		// 1. Read the client's final frame.
		frame := make([]byte, len("last-frame"))
		if _, err := io.ReadFull(reader, frame); err != nil {
			t.Errorf("upstream read frame: %v", err)
			return
		}
		// 2. Client half-close must surface as EOF, not a connection reset.
		tail, err := io.ReadAll(reader)
		if err != nil {
			t.Errorf("upstream half-close read: %v", err)
			return
		}
		if len(tail) != 0 {
			t.Errorf("unexpected tail %q", tail)
		}
		// 3. This direction still works after the peer's half-close.
		conn.Write([]byte("after-half-close"))
	})

	conn, reader := newPipePair(t, upstream, nil)

	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read 101: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}

	if _, err := conn.Write([]byte("last-frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	halfCloser, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatalf("client conn %T does not support CloseWrite", conn)
	}
	if err := halfCloser.CloseWrite(); err != nil {
		t.Fatalf("client half-close: %v", err)
	}

	got := make([]byte, len("after-half-close"))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("read post-half-close frame: %v", err)
	}
	if string(got) != "after-half-close" {
		t.Fatalf("frame = %q", got)
	}
}
