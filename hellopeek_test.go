package hellopeek

import (
	"bytes"
	"crypto/ed25519"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/rand/v2"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Wire-message builders
// ---------------------------------------------------------------------------

func u16(v uint16) []byte {
	return []byte{byte(v >> 8), byte(v)}
}

func vec8(b []byte) []byte {
	return append([]byte{byte(len(b))}, b...)
}

func vec16(b []byte) []byte {
	return append(u16(uint16(len(b))), b...)
}

// record wraps fragment in a TLS record (content type, version 3.3, length).
func record(contentType byte, fragment []byte) []byte {
	r := []byte{contentType, 3, 3, byte(len(fragment) >> 8), byte(len(fragment))}
	return append(r, fragment...)
}

// handshakeMessage wraps a ClientHello body in a handshake header (type 1).
func handshakeMessage(body []byte) []byte {
	n := len(body)
	h := []byte{handshakeClientHello, byte(n >> 16), byte(n >> 8), byte(n)}
	return append(h, body...)
}

func sniExtension(host string) []byte {
	// RFC 6066 wire layout for one host_name:
	//   type(2) | ext_data_len(2) | list_len(2) | name_type(1) | name_len(2) | name
	// There is exactly one length-prefixed list inside extension_data.
	name := []byte(host)
	list := append([]byte{hostNameType}, u16(uint16(len(name)))...)
	list = append(list, name...)
	return append(append(u16(extensionServerName), u16(uint16(2+len(list)))...),
		append(u16(uint16(len(list))), list...)...)
}

func alpnExtension(protos []string) []byte {
	var list []byte
	for _, p := range protos {
		list = append(list, vec8([]byte(p))...)
	}
	// RFC 7301: type(2) | ext_data_len(2) | list_len(2) | ProtocolName...
	return append(append(u16(extensionALPN), u16(uint16(2+len(list)))...),
		append(u16(uint16(len(list))), list...)...)
}

func bodyWithExtensions(exts []byte) []byte {
	var b []byte
	b = append(b, 3, 3)                      // client_version TLS 1.2
	b = append(b, make([]byte, 32)...)       // random
	b = append(b, vec8(make([]byte, 32))...) // session_id
	b = append(b, vec16([]byte{0x13, 0x01, 0x13, 0x02, 0xc0, 0x2f})...)
	b = append(b, vec8([]byte{0})...) // compression_methods
	b = append(b, vec16(exts)...)
	return b
}

func clientHelloBody(sni string, alpn []string) []byte {
	var exts []byte
	if sni != "" {
		exts = append(exts, sniExtension(sni)...)
	}
	if len(alpn) > 0 {
		exts = append(exts, alpnExtension(alpn)...)
	}
	return bodyWithExtensions(exts)
}

// splitIntoRecords wraps the handshake message across several records using
// the given fragment sizes (the last fragment gets the remainder).
func splitIntoRecords(msg []byte, fragSizes ...int) []byte {
	var out []byte
	pos := 0
	for _, size := range fragSizes {
		if pos >= len(msg) {
			break
		}
		end := pos + size
		if end > len(msg) {
			end = len(msg)
		}
		out = append(out, record(contentTypeHandshake, msg[pos:end])...)
		pos = end
	}
	if pos < len(msg) {
		out = append(out, record(contentTypeHandshake, msg[pos:])...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Test fakes
// ---------------------------------------------------------------------------

// chunkReader delivers data in fixed-size chunks, including size 1 for
// byte-at-a-time arrival, regardless of how large the caller's buffer is.
type chunkReader struct {
	data  []byte
	sizes []int
	pos   int
	idx   int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := len(c.data) - c.pos
	if c.idx < len(c.sizes) {
		n = c.sizes[c.idx]
		c.idx++
	}
	if n < 1 {
		n = 1
	}
	if n > len(p) {
		n = len(p)
	}
	if n > len(c.data)-c.pos {
		n = len(c.data) - c.pos
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

type fakeConn struct {
	net.Conn
	r io.Reader
}

func (f *fakeConn) Read(p []byte) (int, error) { return f.r.Read(p) }

// recordingConn captures every byte written to the underlying connection.
type recordingConn struct {
	net.Conn
	sent *bytes.Buffer
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.sent.Write(p)
	return c.Conn.Write(p)
}

// pipeEnd is one endpoint of an in-memory full-duplex connection. The two
// endpoints share two independently locked, buffered streams (one per
// direction). Writes append and return immediately, so a handshake record is
// never blocked waiting for the peer's reader; Close wakes any blocked Read.
type pipeEnd struct {
	recv *stream
	send *stream
}

// stream is a single-direction buffered byte stream with its own condition
// variable. Read blocks only until data or shutdown arrives; Write never
// blocks on a reader because the buffer grows as needed.
type stream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newStream() *stream {
	s := &stream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.buf.Len() == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.buf.Len() == 0 {
		return 0, io.EOF
	}
	return s.buf.Read(p)
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errors.New("stream: write after close")
	}
	n, err := s.buf.Write(p)
	s.cond.Broadcast()
	return n, err
}

func (s *stream) close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

func newBufferedPipe() (net.Conn, net.Conn) {
	aToB, bToA := newStream(), newStream()
	a := &pipeEnd{recv: bToA, send: aToB} // a reads b->a, writes a->b
	b := &pipeEnd{recv: aToB, send: bToA}
	return a, b
}

func (c *pipeEnd) Read(p []byte) (int, error)  { return c.recv.Read(p) }
func (c *pipeEnd) Write(p []byte) (int, error) { return c.send.Write(p) }

func (c *pipeEnd) Close() error {
	c.recv.close()
	c.send.close()
	return nil
}

func (c *pipeEnd) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *pipeEnd) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *pipeEnd) SetDeadline(time.Time) error      { return nil }
func (c *pipeEnd) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeEnd) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "test" }
func (dummyAddr) String() string  { return "in-memory-pipe" }

// streamConn joins two buffered streams into a net.Conn.
type streamConn struct {
	rd *stream
	wr *stream
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.rd.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.wr.Write(p) }
func (c *streamConn) Close() error {
	c.rd.close()
	c.wr.close()
	return nil
}
func (c *streamConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *streamConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

// recordingWriter tees every written byte into a sink for later comparison.
type recordingWriter struct {
	w    io.Writer
	sink *bytes.Buffer
}

func (r *recordingWriter) Write(p []byte) (int, error) {
	r.sink.Write(p)
	return r.w.Write(p)
}

// ---------------------------------------------------------------------------
// Split enumeration: every cut position must give the same result and the
// replayed stream must be byte-identical to what the client sent.
// ---------------------------------------------------------------------------

func verifySplit(t *testing.T, stream []byte, sizes []int, want Info) {
	t.Helper()

	conn := &fakeConn{r: &chunkReader{data: stream, sizes: sizes}}
	info, peeked, err := PeekClientHello(conn)
	if err != nil {
		t.Fatalf("PeekClientHello failed for split %v: %v", sizes, err)
	}
	if info.ServerName != want.ServerName {
		t.Fatalf("SNI = %q, want %q (split %v)", info.ServerName, want.ServerName, sizes)
	}
	if !reflect.DeepEqual(info.ALPNProtocols, want.ALPNProtocols) {
		t.Fatalf("ALPN = %v, want %v (split %v)", info.ALPNProtocols, want.ALPNProtocols, sizes)
	}

	got, err := io.ReadAll(peeked)
	if err != nil {
		t.Fatalf("replay read failed: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("replayed byte stream differs from client stream (split %v)\n got len %d\nwant len %d", sizes, len(got), len(stream))
	}
	if peeked.Buffered() != 0 {
		t.Fatalf("Buffered = %d after full replay, want 0", peeked.Buffered())
	}
}

func verifyEverySplit(t *testing.T, stream []byte, want Info) {
	t.Helper()

	// Every single cut position: a prefix of k bytes then the remainder.
	for cut := 1; cut < len(stream); cut++ {
		verifySplit(t, stream, []int{cut}, want)
	}

	// Byte-at-a-time, including record headers arriving one byte per read.
	ones := make([]int, len(stream))
	for i := range ones {
		ones[i] = 1
	}
	verifySplit(t, stream, ones, want)

	// Random multi-cut partitions.
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 30; iter++ {
		var sizes []int
		remaining := len(stream)
		for remaining > 0 {
			s := rng.IntN(9) + 1
			if s > remaining {
				s = remaining
			}
			sizes = append(sizes, s)
			remaining -= s
		}
		verifySplit(t, stream, sizes, want)
	}
}

func TestPeekSingleRecordEverySplit(t *testing.T) {
	body := clientHelloBody("cdn.example.com", []string{"h2", "http/1.1"})
	stream := record(contentTypeHandshake, handshakeMessage(body))

	want := Info{
		ServerName:    "cdn.example.com",
		ALPNProtocols: []string{"h2", "http/1.1"},
	}
	verifyEverySplit(t, stream, want)
}

func TestPeekHandshakeAcrossRecordsEverySplit(t *testing.T) {
	body := clientHelloBody("multi.example.org", []string{"h3"})
	msg := handshakeMessage(body)

	cases := [][]int{
		{1},             // record header itself is fragmented byte by byte
		{4},             // split right at the handshake header boundary
		{5},             // one byte of the body in the first record
		{7, 3, 11, 1},   // irregular fragments
		{32, 32, 32, 1}, // many small records
	}
	for _, fragSizes := range cases {
		stream := splitIntoRecords(msg, fragSizes...)
		t.Run(fmt.Sprintf("frags%v", fragSizes), func(t *testing.T) {
			verifyEverySplit(t, stream, Info{
				ServerName:    "multi.example.org",
				ALPNProtocols: []string{"h3"},
			})
		})
	}
}

func TestPeekTrailingBytesAreReplayed(t *testing.T) {
	body := clientHelloBody("tail.example.net", []string{"h2"})
	// A full ClientHello record followed by records the peeker must not
	// consume; everything still has to reach the backend unchanged.
	stream := record(contentTypeHandshake, handshakeMessage(body))
	stream = append(stream, record(20, []byte{1})...)          // ChangeCipherSpec
	stream = append(stream, record(23, []byte{0xde, 0xad})...) // application data

	conn := &fakeConn{r: &chunkReader{data: stream, sizes: []int{1}}}
	info, peeked, err := PeekClientHello(conn)
	if err != nil {
		t.Fatalf("PeekClientHello failed: %v", err)
	}
	if info.ServerName != "tail.example.net" {
		t.Fatalf("SNI = %q", info.ServerName)
	}
	if peeked.Buffered() == 0 || peeked.Buffered() >= len(stream) {
		t.Fatalf("Buffered = %d, expected only the ClientHello prefix", peeked.Buffered())
	}

	got, err := io.ReadAll(peeked)
	if err != nil {
		t.Fatalf("replay read failed: %v", err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("replayed stream (len %d) != client stream (len %d)", len(got), len(stream))
	}
}

func TestPeekNoExtensions(t *testing.T) {
	// A minimal TLS 1.2 ClientHello without an extensions block.
	var body []byte
	body = append(body, 3, 3)
	body = append(body, make([]byte, 32)...)
	body = append(body, vec8(nil)...)
	body = append(body, vec16([]byte{0xc0, 0x2f})...)
	body = append(body, vec8([]byte{0})...)

	stream := record(contentTypeHandshake, handshakeMessage(body))
	verifyEverySplit(t, stream, Info{})
}

// ---------------------------------------------------------------------------
// Rejection of malformed and hostile input
// ---------------------------------------------------------------------------

func peekOneByteAtATime(t *testing.T, stream []byte) error {
	t.Helper()
	sizes := make([]int, len(stream))
	for i := range sizes {
		sizes[i] = 1
	}
	_, _, err := PeekClientHello(&fakeConn{r: &chunkReader{data: stream, sizes: sizes}})
	return err
}

func TestPeekRejectsMalformed(t *testing.T) {
	goodBody := clientHelloBody("bad.example.com", []string{"h2"})

	// Handshake header claims a 4-byte body that is too short to hold a
	// ClientHello; the inconsistency is detected by the parser, not by EOF.
	declaredTooShort := record(contentTypeHandshake,
		[]byte{handshakeClientHello, 0, 0, 4, 3, 3, 0, 0})

	// extensions block length overruns the ClientHello body.
	overrunBody := append(append([]byte{}, goodBody...), 0)
	overrunBody[len(overrunBody)-3] = 0xff
	overrunBody[len(overrunBody)-2] = 0xff

	// extensions block length underruns: two extra bytes left over.
	shortExtBody := append(append([]byte{}, goodBody...), 0, 0)

	// The same SNI extension appears twice. Build the outer extensions block
	// explicitly so its declared length matches the two entries.
	dupEntries := append(sniExtension("a.example.com"), sniExtension("b.example.com")...)
	dupBody := bodyWithExtensions(dupEntries)

	// SNI list length does not match the extension data.
	goodList := append([]byte{hostNameType}, u16(1)...) // one 1-byte host "x"
	goodList = append(goodList, 'x')
	badList := append([]byte{0, 2}, goodList...) // claims list length 2, real is 4
	badSNI := append(append(u16(extensionServerName), u16(uint16(len(badList)))...), badList...)
	badSNIBody := bodyWithExtensions(badSNI)

	// Two host_name entries inside one server_name extension.
	hostA := append(append([]byte{hostNameType}, u16(1)...), 'a')
	hostB := append(append([]byte{hostNameType}, u16(1)...), 'b')
	doubleList := append(hostA, hostB...)
	doubleHost := append(append(u16(extensionServerName), u16(uint16(len(doubleList)))...),
		append(u16(uint16(len(doubleList))), doubleList...)...)
	doubleHostBody := bodyWithExtensions(doubleHost)

	// ALPN extension containing an empty protocol name.
	emptyList := []byte{0} // one ProtocolName whose length byte is zero
	emptyALPN := append(append(u16(extensionALPN), u16(uint16(len(emptyList)))...),
		append(u16(uint16(len(emptyList))), emptyList...)...)
	emptyALPNBody := bodyWithExtensions(emptyALPN)

	// session_id of 33 bytes.
	var sid33 []byte
	sid33 = append(sid33, 3, 3)
	sid33 = append(sid33, make([]byte, 32)...)
	sid33 = append(sid33, 33)
	sid33 = append(sid33, make([]byte, 33)...)

	// Record with an oversized fragment (16385 > 2^14).
	bigRecord := []byte{contentTypeHandshake, 3, 3, 0x40, 0x01}
	bigRecord = append(bigRecord, make([]byte, 16385)...)

	// Record layer with the wrong major version.
	badVersion := append([]byte{contentTypeHandshake, 2, 3, 0, 4},
		[]byte{handshakeClientHello, 0, 0, 0}...)

	// ClientHello declaring more than maxClientHelloSize.
	hugeHello := record(contentTypeHandshake, []byte{handshakeClientHello, 0x40, 0, 0})

	// A non-handshake record inserted between two handshake fragments.
	msg := handshakeMessage(goodBody)
	interleaved := record(contentTypeHandshake, msg[:5])
	interleaved = append(interleaved, record(20, []byte{1})...)
	interleaved = append(interleaved, record(contentTypeHandshake, msg[5:])...)

	// First handshake message is not a ClientHello (type 2 = ServerHello).
	wrongType := record(contentTypeHandshake, []byte{2, 0, 0, 1})

	// Truncated mid-record: stream ends before the declared body completes.
	truncated := record(contentTypeHandshake, handshakeMessage(goodBody))
	truncated = truncated[:len(truncated)-5]

	tests := []struct {
		name   string
		stream []byte
		want   error // nil means any non-nil error is acceptable
	}{
		{"first record not handshake", record(23, []byte{1, 2, 3}), ErrNotTLS},
		{"unsupported record version", badVersion, ErrMalformedClientHello},
		{"zero length record", record(contentTypeHandshake, nil), ErrMalformedClientHello},
		{"oversized record fragment", bigRecord, ErrMalformedClientHello},
		{"interleaved non-handshake record", interleaved, ErrNonHandshakeRecord},
		{"wrong handshake message type", wrongType, ErrMalformedClientHello},
		{"handshake length inconsistency", declaredTooShort, ErrMalformedClientHello},
		{"extensions length overruns body", record(contentTypeHandshake, handshakeMessage(overrunBody)), ErrMalformedClientHello},
		{"extensions length underruns body", record(contentTypeHandshake, handshakeMessage(shortExtBody)), ErrMalformedClientHello},
		{"duplicate extension", record(contentTypeHandshake, handshakeMessage(dupBody)), ErrDuplicateExtension},
		{"sni list length mismatch", record(contentTypeHandshake, handshakeMessage(badSNIBody)), ErrMalformedClientHello},
		{"duplicate sni host_name", record(contentTypeHandshake, handshakeMessage(doubleHostBody)), ErrMalformedClientHello},
		{"empty alpn protocol", record(contentTypeHandshake, handshakeMessage(emptyALPNBody)), ErrMalformedClientHello},
		{"session id too long", record(contentTypeHandshake, handshakeMessage(sid33)), ErrMalformedClientHello},
		{"clienthello too large", hugeHello, ErrClientHelloTooLarge},
		{"truncated stream", truncated, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := peekOneByteAtATime(t, tc.stream)
			if err == nil {
				t.Fatalf("expected rejection for %q, got nil", tc.name)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// End-to-end test against crypto/tls: the gateway peeks without terminating
// TLS, replays buffered bytes, and a real TLS server completes the handshake.
// ---------------------------------------------------------------------------

func localCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sni.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"sni.example.com"},
	}
	der, err := x509.CreateCertificate(crand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

func TestEndToEndTLSThroughPeeker(t *testing.T) {
	cert := localCertificate(t)
	clientPipe, gatewayPipe := newBufferedPipe()

	var clientSent bytes.Buffer
	clientConn := &recordingConn{Conn: clientPipe, sent: &clientSent}

	type peekResult struct {
		info *Info
		conn *Conn
		err  error
	}
	peekedCh := make(chan peekResult, 1)
	go func() {
		info, peeked, err := PeekClientHello(gatewayPipe)
		peekedCh <- peekResult{info, peeked, err}
	}()

	clientTLS := tls.Client(clientConn, &tls.Config{
		ServerName:         "sni.example.com",
		NextProtos:         []string{"h2", "http/1.1"},
		InsecureSkipVerify: true,
	})
	clientErr := make(chan error, 1)
	go func() { clientErr <- clientTLS.Handshake() }()

	// The peeker consumes the full ClientHello while the client handshake is
	// blocked waiting for a ServerHello.
	res := <-peekedCh
	if res.err != nil {
		t.Fatalf("PeekClientHello failed: %v", res.err)
	}
	if res.info.ServerName != "sni.example.com" {
		t.Fatalf("peeked SNI = %q, want sni.example.com", res.info.ServerName)
	}
	wantALPN := []string{"h2", "http/1.1"}
	if !reflect.DeepEqual(res.info.ALPNProtocols, wantALPN) {
		t.Fatalf("peeked ALPN = %v, want %v", res.info.ALPNProtocols, wantALPN)
	}
	if res.conn.Buffered() == 0 {
		t.Fatal("expected buffered ClientHello bytes after peek")
	}

	// Backend link: two independent buffered streams, one per direction.
	//   c2b carries client->backend bytes the gateway forwards.
	//   b2c carries backend->client replies.
	c2b, b2c := newStream(), newStream()
	var backendRecv bytes.Buffer
	beServerConn := &streamConn{rd: c2b, wr: b2c}
	beClientWriter := &recordingWriter{w: c2b, sink: &backendRecv}

	// Start both bridges before any handshake: forward the replayed prefix
	// plus the live client stream toward the backend, and pump backend
	// replies back toward the client.
	var bridges sync.WaitGroup
	bridges.Add(2)
	go func() {
		defer bridges.Done()
		_, _ = io.Copy(beClientWriter, res.conn)
		c2b.close()
	}()
	go func() {
		defer bridges.Done()
		// Return traffic must enter the gateway endpoint's send stream so it
		// lands on the client endpoint's receive stream.
		_, _ = io.Copy(gatewayPipe, b2c)
	}()

	serverTLS := tls.Server(beServerConn, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	})
	if err := serverTLS.Handshake(); err != nil {
		t.Fatalf("backend TLS handshake failed: %v", err)
	}
	if err := <-clientErr; err != nil {
		t.Fatalf("client TLS handshake failed: %v", err)
	}
	if serverTLS.ConnectionState().ServerName != "sni.example.com" {
		t.Fatalf("backend SNI = %q", serverTLS.ConnectionState().ServerName)
	}
	if serverTLS.ConnectionState().NegotiatedProtocol != "h2" {
		t.Fatalf("backend ALPN = %q, want h2", serverTLS.ConnectionState().NegotiatedProtocol)
	}

	// Application data must survive the peek-and-replay path untouched.
	payload := []byte("hello through the gateway without TLS termination")
	echoCh := make(chan error, 1)
	go func() {
		buf := make([]byte, len(payload))
		_, err := io.ReadFull(serverTLS, buf)
		if err == nil && !bytes.Equal(buf, payload) {
			err = fmt.Errorf("backend got %q, want %q", buf, payload)
		}
		echoCh <- err
	}()
	if _, err := clientTLS.Write(payload); err != nil {
		t.Fatalf("client write: %v", err)
	}
	if err := <-echoCh; err != nil {
		t.Fatal(err)
	}

	// At this point every client record (ClientHello through app data) has
	// traversed the gateway; the backend must see the identical wire stream.
	if !bytes.Equal(backendRecv.Bytes(), clientSent.Bytes()) {
		t.Fatalf("backend byte stream (%d bytes) != client wire stream (%d bytes)",
			backendRecv.Len(), clientSent.Len())
	}

	// Tear down cleanly: close the TLS endpoints so close_notify is
	// forwarded, then close the raw links so blocking bridge copies observe
	// EOF and the WaitGroup drains instead of leaking goroutines.
	_ = clientTLS.Close()
	_ = serverTLS.Close()

	// Closing the client link unblocks the forward bridge's read; closing
	// the backend writer unblocks the reverse bridge once replies are drained.
	_ = clientPipe.Close()
	_ = gatewayPipe.Close()
	c2b.close()
	b2c.close()
	bridges.Wait()
}
