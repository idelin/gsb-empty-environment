// Package wsupgrade implements a reverse-proxy WebSocket upgrade handler that
// only switches to a bidirectional tunnel after strictly validating the
// upstream upgrade response.
//
// A naive proxy that hijacks the client connection as soon as it sees an
// Upgrade request header can be tricked into request smuggling: if the upstream
// answers with a normal response (200/426/...) or with a malformed 101, the
// already-hijacked client socket cannot be returned to the normal HTTP path and
// any bytes the client pipelined may be interpreted as a fresh request by the
// next hop. This handler therefore keeps the client connection on the regular
// HTTP path until the upstream response has been fully verified, and only then
// hijacks it. Bytes buffered in either direction before the switch are carried
// over into the tunnel so nothing is lost or delivered to the wrong peer.
package wsupgrade

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// websocketGUID is the magic GUID from RFC 6455 section 4.2.2, appended to the
// client Sec-WebSocket-Key to derive Sec-WebSocket-Accept.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// hopByHopHeaders are per-connection headers (RFC 7230 6.1) that must not be
// forwarded by a proxy.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Proxy is a minimal reverse proxy with a hardened WebSocket upgrade path.
//
// Target is the upstream base URL; its scheme determines whether the upstream
// dial uses TLS (wss/https) or plain TCP (ws/http).
type Proxy struct {
	// Target is the base URL of the upstream server.
	Target *url.URL

	// DialTimeout bounds the upstream dial for an upgraded connection. Zero
	// means a sensible default is used.
	DialTimeout time.Duration

	// DialContext optionally overrides the transport dial used for upgraded
	// connections. When nil a normal TCP dial (plus TLS for wss/https) is used.
	// It is mainly a seam for in-process testing.
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewProxy builds a Proxy for the given upstream base URL.
func NewProxy(target *url.URL) *Proxy {
	return &Proxy{Target: target}
}

// ServeHTTP dispatches WebSocket upgrade requests to the hardened handler and
// forwards everything else as an ordinary reverse-proxy request.
func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if isUpgradeRequest(req) {
		p.proxyUpgrade(rw, req)
		return
	}
	p.forwardPlain(rw, req)
}

// isUpgradeRequest reports whether req asks for a protocol upgrade, i.e. its
// Connection header carries the "upgrade" token and an Upgrade header exists.
func isUpgradeRequest(req *http.Request) bool {
	return headerHasToken(req.Header, "Connection", "upgrade") && req.Header.Get("Upgrade") != ""
}

// proxyUpgrade performs the validated upgrade.
//
// The client connection is never hijacked before the upstream 101 response is
// read and verified. On any failure the client gets an ordinary HTTP response
// (the real upstream response for non-101 statuses, a 502 for a malformed 101)
// and the connection is closed, so pipelined client bytes cannot be smuggled.
func (p *Proxy) proxyUpgrade(rw http.ResponseWriter, req *http.Request) {
	upstream, upReader, err := p.dialUpstream(req)
	if err != nil {
		reject(rw, http.StatusBadGateway, "websocket proxy: dial upstream: "+err.Error())
		return
	}
	defer upstream.Close()

	if err := writeUpstreamRequest(req, p.Target, upstream); err != nil {
		reject(rw, http.StatusBadGateway, "websocket proxy: write upgrade request: "+err.Error())
		return
	}

	resp, err := http.ReadResponse(upReader.Reader, req)
	if err != nil {
		reject(rw, http.StatusBadGateway, "websocket proxy: read upstream response: "+err.Error())
		return
	}

	// Non-101 answers are ordinary HTTP responses. Forward them verbatim and
	// close the client connection afterwards; never open a tunnel.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		markConnectionClose(rw, req)
		forwardPlainResponse(rw, resp)
		return
	}

	// A 101 must satisfy every WebSocket invariant before we trust it.
	if err := validateUpgradeResponse(req, resp); err != nil {
		resp.Body.Close()
		markConnectionClose(rw, req)
		reject(rw, http.StatusBadGateway, "websocket proxy: invalid upstream upgrade: "+err.Error())
		return
	}

	// Everything checks out: from this point on we own the client socket.
	hj, ok := rw.(http.Hijacker)
	if !ok {
		reject(rw, http.StatusInternalServerError, "websocket proxy: response writer does not support hijacking")
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		reject(rw, http.StatusInternalServerError, "websocket proxy: hijack: "+err.Error())
		return
	}
	defer clientConn.Close()

	// Replay the validated 101 to the client. Body is empty for a 101; set it
	// to nil so Response.Write emits headers only.
	resp.Body = nil
	if err := resp.Write(clientBuf); err != nil {
		return
	}
	if err := clientBuf.Flush(); err != nil {
		return
	}

	// Wrap both sockets so bytes already buffered in the proxy's readers are
	// drained before reading from the underlying connections. This guarantees
	// pre-upgrade frames cannot be lost or sent to the wrong peer.
	clientSide := &bufferedConn{Conn: clientConn, buffered: clientBuf.Reader}
	upstreamSide := &bufferedConn{Conn: upstream, buffered: upReader.Reader}

	tunnel(clientSide, upstreamSide)
}

// dialUpstream opens the raw transport connection to the upstream and returns
// it together with a buffered reader used for the HTTP response and, later, the
// tunnel.
func (p *Proxy) dialUpstream(req *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	timeout := p.DialTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	host := p.Target.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		if useTLS(p.Target.Scheme) {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}

	dial := p.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: timeout}
		dial = dialer.DialContext
	}
	raw, err := dial(req.Context(), "tcp", host)
	if err != nil {
		return nil, nil, err
	}

	var conn net.Conn = raw
	if useTLS(p.Target.Scheme) {
		serverName := p.Target.Hostname()
		tlsConn := tls.Client(raw, &tls.Config{ServerName: serverName})
		ctx, cancel := context.WithTimeout(req.Context(), timeout)
		defer cancel()
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, nil, err
		}
		conn = tlsConn
	}

	reader := bufio.NewReader(conn)
	return conn, bufio.NewReadWriter(reader, bufio.NewWriter(conn)), nil
}

// useTLS reports whether the upstream scheme requires a TLS handshake.
func useTLS(scheme string) bool {
	return scheme == "https" || scheme == "wss"
}

// writeUpstreamRequest serializes the client's upgrade request onto the raw
// upstream connection.
func writeUpstreamRequest(req *http.Request, target *url.URL, w io.Writer) error {
	out := req.Clone(req.Context())
	out.RequestURI = ""

	scheme := target.Scheme
	if scheme == "ws" {
		scheme = "http"
	} else if scheme == "wss" {
		scheme = "https"
	}
	out.URL.Scheme = scheme
	out.URL.Host = target.Host

	removeHopByHop(out.Header)
	// WebSocket upgrade requests carry their own hop-by-hop negotiation headers.
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", req.Header.Get("Upgrade"))

	return out.Write(w)
}

// validateUpgradeResponse enforces RFC 6455 4.2.2 plus the protocol invariants
// needed to rule out a spoofed switch:
//   - Connection header contains the "upgrade" token,
//   - Upgrade header matches the requested protocol (case-insensitive),
//   - Sec-WebSocket-Accept equals SHA-1(key + GUID) base64-encoded.
func validateUpgradeResponse(req *http.Request, resp *http.Response) error {
	if !headerHasToken(resp.Header, "Connection", "upgrade") {
		return errors.New("missing Connection: upgrade token")
	}

	requested := strings.TrimSpace(req.Header.Get("Upgrade"))
	got := strings.TrimSpace(resp.Header.Get("Upgrade"))
	if !strings.EqualFold(requested, got) {
		return errors.New("Upgrade protocol mismatch: got " + got + ", want " + requested)
	}

	key := req.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return errors.New("request has no Sec-WebSocket-Key")
	}
	wantAccept := secWebSocketAccept(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != wantAccept {
		return errors.New("Sec-WebSocket-Accept mismatch")
	}
	return nil
}

// secWebSocketAccept computes the expected Sec-WebSocket-Accept value.
func secWebSocketAccept(clientKey string) string {
	sum := sha1.Sum([]byte(clientKey + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// tunnel pumps bytes in both directions until either side ends or errors.
// Half-close is honored: when one direction reaches EOF the write side of the
// peer is shut down while the other direction keeps flowing, mirroring TCP
// half-close semantics for WebSocket frames.
func tunnel(a, b closeWriter) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		copyAndClose(b, a)
	}()
	go func() {
		defer wg.Done()
		copyAndClose(a, b)
	}()

	wg.Wait()
}

// closeWriter is a net.Conn that can also half-close its write side.
type closeWriter interface {
	net.Conn
	CloseWrite() error
}

// copyAndClose copies from src to dst and, on EOF, half-closes dst so the peer
// observes end of stream without tearing down the whole socket. Any error
// (including a full close) simply ends this direction.
func copyAndClose(dst closeWriter, src io.Reader) {
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return
	}
	_ = dst.CloseWrite()
}

// bufferedConn presents a net.Conn whose Reads first drain bytes already
// captured in a bufio.Reader before touching the underlying connection. Writes
// and deadlines go straight through.
type bufferedConn struct {
	net.Conn
	buffered *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	// The bufio.Reader drains any pre-upgrade buffered bytes first and then
	// transparently fills from the underlying connection once empty.
	return c.buffered.Read(p)
}

// CloseWrite propagates a half-close to the underlying connection.
func (c *bufferedConn) CloseWrite() error {
	if cw, ok := c.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

// forwardPlain handles non-upgrade requests through the default transport.
func (p *Proxy) forwardPlain(rw http.ResponseWriter, req *http.Request) {
	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.URL.Scheme = p.Target.Scheme
	out.URL.Host = p.Target.Host
	removeHopByHop(out.Header)

	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		reject(rw, http.StatusBadGateway, "reverse proxy: upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()
	forwardPlainResponse(rw, resp)
}

// forwardPlainResponse copies an ordinary upstream response (status, headers and
// body) back to the client without hijacking the connection.
func forwardPlainResponse(rw http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	removeHopByHop(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			rw.Header().Add(key, value)
		}
	}
	rw.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(rw, resp.Body)
}

// reject writes a plain-text error response.
func reject(rw http.ResponseWriter, status int, message string) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(status)
	_, _ = io.WriteString(rw, http.StatusText(status)+": "+message+"\n")
}

// markConnectionClose forces the server to close the client connection once
// the current response is done, preventing connection reuse after a rejected
// upgrade and so neutralizing pipelined/smuggled requests.
func markConnectionClose(rw http.ResponseWriter, req *http.Request) {
	req.Close = true
	rw.Header().Set("Connection", "close")
}

// removeHopByHop strips connection-scoped headers.
func removeHopByHop(header http.Header) {
	for _, name := range hopByHopHeaders {
		header.Del(name)
	}
}

// headerHasToken reports whether the given header contains the token, matching
// comma-separated tokens case-insensitively (RFC 7230).
func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header[http.CanonicalHeaderKey(name)] {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
