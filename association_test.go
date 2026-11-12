package netsec

import (
	"errors"
	"net"
	"testing"
)

var (
	controlConn = &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 51000}
	declaredAny = &net.UDPAddr{IP: net.IPv4zero, Port: 0} // client declares 0.0.0.0:0
)

// udpRequest builds a SOCKS5 UDP request datagram for an IPv4 target.
func udpRequest(frag byte, ip string, port uint16, payload string) []byte {
	d := []byte{0, 0, frag, 0x01}
	d = append(d, net.ParseIP(ip).To4()...)
	d = append(d, byte(port>>8), byte(port))
	return append(d, payload...)
}

func newAssoc(t *testing.T, declared *net.UDPAddr, blocked []*net.IPNet) (*Association, chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	return NewAssociation(declared, controlConn, done, blocked), done
}

func TestAdmitHappyPath(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	target, payload, err := a.Admit(src, udpRequest(0, "8.8.8.8", 53, "dns-query"))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if got := target.String(); got != "8.8.8.8:53" {
		t.Fatalf("target = %s", got)
	}
	if string(payload) != "dns-query" {
		t.Fatalf("payload = %q", payload)
	}
	if got := a.ClientAddr().String(); got != "203.0.113.7:40000" {
		t.Fatalf("pinned client = %s", got)
	}
}

// An internet attacker sprays the relay port with a spoofed source:
// any IP other than the declared/TCP peer must be dropped.
func TestForgedExternalSource(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	attacker := &net.UDPAddr{IP: net.ParseIP("198.51.100.66"), Port: 40000}
	if _, _, err := a.Admit(attacker, udpRequest(0, "8.8.8.8", 53, "x")); !errors.Is(err, ErrBadSource) {
		t.Fatalf("forged source: got %v, want ErrBadSource", err)
	}
	// Even the same IP with the wrong first port after pinning fails.
	good := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(good, udpRequest(0, "8.8.8.8", 53, "x")); err != nil {
		t.Fatalf("legit client: %v", err)
	}
	if _, _, err := a.Admit(attacker, udpRequest(0, "8.8.8.8", 53, "x")); !errors.Is(err, ErrBadSource) {
		t.Fatalf("forged source after pin: got %v, want ErrBadSource", err)
	}
}

// FRAG != 0 must be dropped: fragmentation is not supported.
func TestFragmentedDatagram(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(src, udpRequest(1, "8.8.8.8", 53, "x")); !errors.Is(err, ErrFragmented) {
		t.Fatalf("FRAG=1: got %v, want ErrFragmented", err)
	}
	if _, _, err := a.Admit(src, udpRequest(127, "8.8.8.8", 53, "x")); !errors.Is(err, ErrFragmented) {
		t.Fatalf("FRAG=127: got %v, want ErrFragmented", err)
	}
}

// Once the first datagram pins the client port, later datagrams from
// the same IP but a different port must be dropped.
func TestPortDrift(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	first := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(first, udpRequest(0, "8.8.8.8", 53, "x")); err != nil {
		t.Fatalf("first datagram: %v", err)
	}
	drifted := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40001}
	if _, _, err := a.Admit(drifted, udpRequest(0, "8.8.8.8", 53, "x")); !errors.Is(err, ErrBadSource) {
		t.Fatalf("port drift: got %v, want ErrBadSource", err)
	}
	// Original port still works.
	if _, _, err := a.Admit(first, udpRequest(0, "1.1.1.1", 53, "x")); err != nil {
		t.Fatalf("pinned port: %v", err)
	}
}

// The association dies with the TCP control connection: datagrams
// arriving afterwards, even from the legitimate client, are dropped.
func TestControlConnectionClosed(t *testing.T) {
	a, done := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(src, udpRequest(0, "8.8.8.8", 53, "x")); err != nil {
		t.Fatalf("before close: %v", err)
	}
	close(done)
	if a.Alive() {
		t.Fatal("association still alive after control connection closed")
	}
	if _, _, err := a.Admit(src, udpRequest(0, "8.8.8.8", 53, "x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: got %v, want ErrClosed", err)
	}
	if dst, dg := a.BuildReply(Address{IP: net.ParseIP("8.8.8.8"), Port: 53}, []byte("x")); dst != nil || dg != nil {
		t.Fatal("reply built after control connection closed")
	}
}

// Non-zero RSV bytes must be dropped.
func TestReservedField(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	d := udpRequest(0, "8.8.8.8", 53, "x")
	d[0] = 0xff
	if _, _, err := a.Admit(src, d); !errors.Is(err, ErrReserved) {
		t.Fatalf("RSV[0]=0xff: got %v, want ErrReserved", err)
	}
	d[0], d[1] = 0, 0x01
	if _, _, err := a.Admit(src, d); !errors.Is(err, ErrReserved) {
		t.Fatalf("RSV[1]=0x01: got %v, want ErrReserved", err)
	}
}

// A domain length that runs past the end of the datagram must be
// dropped, not read out of bounds.
func TestDomainLengthOutOfBounds(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	// Claims a 200-byte domain but carries only 3 bytes.
	d := []byte{0, 0, 0, 0x03, 200, 'a', 'b', 'c'}
	if _, _, err := a.Admit(src, d); !errors.Is(err, ErrMalformed) {
		t.Fatalf("oversized domain: got %v, want ErrMalformed", err)
	}
	// Domain fits but the port field is missing.
	d = []byte{0, 0, 0, 0x03, 3, 'a', 'b', 'c'}
	if _, _, err := a.Admit(src, d); !errors.Is(err, ErrMalformed) {
		t.Fatalf("missing port: got %v, want ErrMalformed", err)
	}
	// Truncated header and unknown ATYP.
	if _, _, err := a.Admit(src, []byte{0, 0, 0}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("short header: got %v, want ErrMalformed", err)
	}
	if _, _, err := a.Admit(src, []byte{0, 0, 0, 0x05, 1, 2, 3, 4, 5, 6}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad ATYP: got %v, want ErrMalformed", err)
	}
}

// A valid domain target parses cleanly.
func TestDomainTarget(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	d := []byte{0, 0, 0, 0x03, 11}
	d = append(d, "example.com"...)
	d = append(d, 0x01, 0xbb, 'h', 'i')
	target, payload, err := a.Admit(src, d)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if target.Domain != "example.com" || target.Port != 443 || string(payload) != "hi" {
		t.Fatalf("target = %+v payload %q", target, payload)
	}
}

// Declaring 0.0.0.0 falls back to the TCP control connection's source
// address; an explicit declaration is honored instead.
func TestDeclaredAddressFallback(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	if got := a.clientIP.String(); got != "203.0.113.7" {
		t.Fatalf("fallback client IP = %s", got)
	}
	explicit := &net.UDPAddr{IP: net.ParseIP("192.0.2.9"), Port: 0}
	b, _ := newAssoc(t, explicit, nil)
	if got := b.clientIP.String(); got != "192.0.2.9" {
		t.Fatalf("declared client IP = %s", got)
	}
	// The TCP peer itself is now the "forged" source.
	if _, _, err := b.Admit(&net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 1},
		udpRequest(0, "8.8.8.8", 53, "x")); !errors.Is(err, ErrBadSource) {
		t.Fatalf("tcp peer vs declared: got %v, want ErrBadSource", err)
	}
}

// Targets inside caller-forbidden networks are dropped before relay.
func TestBlockedTargetNetwork(t *testing.T) {
	_, blocked, _ := net.ParseCIDR("10.0.0.0/8")
	a, _ := newAssoc(t, declaredAny, []*net.IPNet{blocked})
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(src, udpRequest(0, "10.1.2.3", 80, "x")); !errors.Is(err, ErrBlockedTarget) {
		t.Fatalf("internal target: got %v, want ErrBlockedTarget", err)
	}
	if _, _, err := a.Admit(src, udpRequest(0, "8.8.8.8", 53, "x")); err != nil {
		t.Fatalf("public target: %v", err)
	}
}

// Return traffic is addressed only to the pinned client, and to no
// one before the port is pinned.
func TestReplyGoesOnlyToClient(t *testing.T) {
	a, _ := newAssoc(t, declaredAny, nil)
	if dst, dg := a.BuildReply(Address{IP: net.ParseIP("8.8.8.8"), Port: 53}, []byte("x")); dst != nil || dg != nil {
		t.Fatal("reply built before client port pinned")
	}
	src := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 40000}
	if _, _, err := a.Admit(src, udpRequest(0, "8.8.8.8", 53, "x")); err != nil {
		t.Fatalf("admit: %v", err)
	}
	dst, dg := a.BuildReply(Address{IP: net.ParseIP("8.8.8.8"), Port: 53}, []byte("pong"))
	if dst == nil || dst.String() != "203.0.113.7:40000" {
		t.Fatalf("reply dst = %v", dst)
	}
	target, payload, err := parseRequest(dg)
	if err != nil || target.String() != "8.8.8.8:53" || string(payload) != "pong" {
		t.Fatalf("reply datagram unparses to %v %q %v", target, payload, err)
	}
}
