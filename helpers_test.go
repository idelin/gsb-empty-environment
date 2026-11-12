package netsec

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var fakeHopCounter atomic.Int32

type memoryAddr string

func (a memoryAddr) Network() string { return "memory" }
func (a memoryAddr) String() string  { return string(a) }

// bufferedConnPair provides two connected net.Conns with buffered writes:
// Write appends to the peer's read buffer instead of blocking. net.Pipe is
// unbuffered and can deadlock when both TLS endpoints send handshake flights
// at the same time.
type bufferedConn struct {
	mu     sync.Mutex
	cond   *sync.Cond
	rbuf   []byte
	peer   *bufferedConn
	closed bool
	local  memoryAddr
	remote memoryAddr
}

func newBufferedConnPair() (net.Conn, net.Conn) {
	a := &bufferedConn{local: "client", remote: "server"}
	b := &bufferedConn{local: "server", remote: "client"}
	a.cond = sync.NewCond(&a.mu)
	b.cond = sync.NewCond(&b.mu)
	a.peer = b
	b.peer = a
	return a, b
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.rbuf) == 0 && !c.closed {
		c.cond.Wait()
	}
	if len(c.rbuf) == 0 {
		if c.closed {
			return 0, io.EOF
		}
		return 0, nil
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *bufferedConn) Write(p []byte) (int, error) {
	peer := c.peer
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.closed {
		return 0, net.ErrClosed
	}
	peer.rbuf = append(peer.rbuf, p...)
	peer.cond.Signal()
	return len(p), nil
}

func (c *bufferedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()

	peer := c.peer
	peer.mu.Lock()
	peer.closed = true
	peer.cond.Broadcast()
	peer.mu.Unlock()
	return nil
}

func (c *bufferedConn) LocalAddr() net.Addr              { return c.local }
func (c *bufferedConn) RemoteAddr() net.Addr             { return c.remote }
func (c *bufferedConn) SetDeadline(time.Time) error      { return nil }
func (c *bufferedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *bufferedConn) SetWriteDeadline(time.Time) error { return nil }

func testCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mx.test"},
		DNSNames:              []string{"mx.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

type recordedCommand struct {
	Line string
	TLS  bool
}

type fakeNextHopConfig struct {
	OfferSTARTTLS             bool
	RejectSTARTTLS            bool
	AdvertiseRequireTLSPreTLS bool
	AdvertiseRequireTLS       bool
}

type fakeNextHop struct {
	t    *testing.T
	cfg  fakeNextHopConfig
	cert tls.Certificate

	server   net.Conn
	client   net.Conn
	addr     string
	mu       sync.Mutex
	commands []recordedCommand
	data     []byte
	done     chan struct{}
}

func newFakeNextHop(t *testing.T, cfg fakeNextHopConfig) *fakeNextHop {
	t.Helper()
	server, client := newBufferedConnPair()
	h := &fakeNextHop{
		t:      t,
		cfg:    cfg,
		cert:   testCertificate(t),
		server: server,
		client: client,
		addr:   "mx" + strconv.Itoa(int(fakeHopCounter.Add(1))) + ".test:25",
		done:   make(chan struct{}),
	}
	go h.serve(server)
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return h
}

func (h *fakeNextHop) address() string {
	return h.addr
}

func (h *fakeNextHop) dial(context.Context, string, string) (net.Conn, error) {
	return h.client, nil
}

func (h *fakeNextHop) snapshot() ([]recordedCommand, []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cmds := append([]recordedCommand(nil), h.commands...)
	data := append([]byte(nil), h.data...)
	return cmds, data
}

func (h *fakeNextHop) record(line string, tlsActive bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commands = append(h.commands, recordedCommand{Line: line, TLS: tlsActive})
}

func (h *fakeNextHop) serve(conn net.Conn) {
	defer close(h.done)
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	_ = writeReply(w, 220, "mx.test ESMTP")

	tlsActive := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		h.record(line, tlsActive)
		cmd, arg := splitCommand(line)
		switch cmd {
		case "EHLO":
			advertiseRequireTLS := h.cfg.AdvertiseRequireTLSPreTLS
			if tlsActive {
				advertiseRequireTLS = h.cfg.AdvertiseRequireTLS
			}
			if err := h.writeEHLO(w, !tlsActive && h.cfg.OfferSTARTTLS, advertiseRequireTLS); err != nil {
				return
			}
		case "HELO":
			if err := writeReply(w, 250, "mx.test"); err != nil {
				return
			}
		case "STARTTLS":
			if arg != "" || !h.cfg.OfferSTARTTLS || tlsActive || h.cfg.RejectSTARTTLS {
				if err := writeReply(w, 502, "5.5.1 STARTTLS unavailable"); err != nil {
					return
				}
				continue
			}
			if err := writeReply(w, 220, "2.0.0 go ahead"); err != nil {
				return
			}
			tlsConn := tls.Server(conn, &tls.Config{
				Certificates: []tls.Certificate{h.cert},
				MinVersion:   tls.VersionTLS12,
			})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			tlsActive = true
		case "MAIL", "RCPT":
			if err := writeReply(w, 250, "2.1.0 ok"); err != nil {
				return
			}
		case "DATA":
			if err := writeReply(w, 354, "3.0.0 send data"); err != nil {
				return
			}
			data, _, err := readMessageData(r, 1<<20)
			if err != nil {
				return
			}
			h.mu.Lock()
			h.data = data
			h.mu.Unlock()
			if err := writeReply(w, 250, "2.0.0 queued"); err != nil {
				return
			}
		case "QUIT":
			_ = writeReply(w, 221, "2.0.0 bye")
			return
		default:
			if err := writeReply(w, 500, "5.5.1 unrecognized"); err != nil {
				return
			}
		}
	}
}

func (h *fakeNextHop) writeEHLO(w *bufio.Writer, offerStartTLS, advertiseRequireTLS bool) error {
	lines := []string{"mx.test", "SIZE 1048576", "8BITMIME"}
	if advertiseRequireTLS {
		lines = append(lines, "REQUIRETLS")
	}
	if offerStartTLS {
		lines = append(lines, "STARTTLS")
	}
	for i, line := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		if _, err := w.WriteString("250" + sep + line + "\r\n"); err != nil {
			return err
		}
	}
	return w.Flush()
}

type testSMTPClient struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func newTestSMTPClient(t *testing.T, conn net.Conn) *testSMTPClient {
	t.Helper()
	c := &testSMTPClient{
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
	}
	t.Cleanup(func() { _ = c.conn.Close() })
	return c
}

func (c *testSMTPClient) read() smtpReply {
	return mustReply(c.r)
}

func (c *testSMTPClient) send(line string) smtpReply {
	if _, err := c.w.WriteString(line + "\r\n"); err != nil {
		panic(err)
	}
	if err := c.w.Flush(); err != nil {
		panic(err)
	}
	return mustReply(c.r)
}

func (c *testSMTPClient) startTLS() {
	if reply := c.send("STARTTLS"); reply.code != 220 {
		panic("STARTTLS refused: " + reply.text())
	}
	tlsConn := tls.Client(c.conn, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "mx.test",
		MinVersion:         tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		panic(err)
	}
	c.conn = tlsConn
	c.r = bufio.NewReader(tlsConn)
	c.w = bufio.NewWriter(tlsConn)
}

func mustReply(r *bufio.Reader) smtpReply {
	reply, err := readReply(r)
	if err != nil {
		panic(err)
	}
	return reply
}

func startInboundServer(t *testing.T, tlsConfig *tls.Config, handler InboundHandler) net.Conn {
	t.Helper()
	server, client := newBufferedConnPair()
	srv := &InboundServer{
		Hostname:  "relay.test",
		TLSConfig: tlsConfig,
		Handler:   handler,
	}
	go func() {
		_ = srv.ServeConn(server)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return client
}
