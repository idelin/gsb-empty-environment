package netsec

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeControlConn struct {
	net.Conn
	remote net.Addr
}

func (c *fakeControlConn) RemoteAddr() net.Addr {
	return c.remote
}

func tcpControlPair(t *testing.T) (server, client net.Conn) {
	t.Helper()

	serverPipe, client := net.Pipe()
	server = &fakeControlConn{
		Conn:   serverPipe,
		remote: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 55000},
	}
	t.Cleanup(func() {
		server.Close()
		client.Close()
	})
	return server, client
}

type capturedWrite struct {
	packet []byte
	addr   net.Addr
}

type capturePacketConn struct {
	mu     sync.Mutex
	writes []capturedWrite
}

func (c *capturePacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, io.EOF
}

func (c *capturePacketConn) WriteTo(packet []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, capturedWrite{
		packet: append([]byte(nil), packet...),
		addr:   addr,
	})
	return len(packet), nil
}

func (c *capturePacketConn) Close() error                     { return nil }
func (c *capturePacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *capturePacketConn) SetDeadline(time.Time) error      { return nil }
func (c *capturePacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *capturePacketConn) SetWriteDeadline(time.Time) error { return nil }

func (c *capturePacketConn) snapshot() []capturedWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedWrite(nil), c.writes...)
}

func mustAssociation(t *testing.T, control net.Conn, requested *net.UDPAddr, policy Policy) *Association {
	t.Helper()
	association, err := NewAssociationWithPolicy(control, requested, policy)
	if err != nil {
		t.Fatalf("NewAssociationWithPolicy: %v", err)
	}
	t.Cleanup(func() {
		association.Close()
	})
	return association
}

func mustDatagram(t *testing.T, target Address, payload string) []byte {
	t.Helper()
	packet, err := EncodeDatagram(target, []byte(payload))
	if err != nil {
		t.Fatalf("EncodeDatagram: %v", err)
	}
	return packet
}

func udpSource(ip string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

func waitAssociationDone(t *testing.T, association *Association) {
	t.Helper()
	select {
	case <-association.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("association did not end after control connection closed")
	}
}

func TestAdmitRejectsForgedSourceAndPinsPort(t *testing.T) {
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{})
	packet := mustDatagram(t, Address{Host: "192.0.2.10", Port: 53}, "query")

	if _, _, err := association.Admit(udpSource("127.0.0.2", 40000), packet); !errors.Is(err, ErrSourceAddress) {
		t.Fatalf("forged IP error = %v, want ErrSourceAddress", err)
	}

	target, payload, err := association.Admit(udpSource("127.0.0.1", 40000), packet)
	if err != nil {
		t.Fatalf("legitimate datagram rejected: %v", err)
	}
	if target != (Address{Host: "192.0.2.10", Port: 53}) || string(payload) != "query" {
		t.Fatalf("admitted target/payload = %v/%q", target, payload)
	}
	if got := association.ClientAddr(); got.IP.String() != "127.0.0.1" || got.Port != 40000 {
		t.Fatalf("bound client = %v, want 127.0.0.1:40000", got)
	}

	if _, _, err := association.Admit(udpSource("127.0.0.2", 40000), packet); !errors.Is(err, ErrSourceAddress) {
		t.Fatalf("forged IP after pin error = %v, want ErrSourceAddress", err)
	}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 40001), packet); !errors.Is(err, ErrSourcePort) {
		t.Fatalf("port drift error = %v, want ErrSourcePort", err)
	}
	if got := association.ClientAddr(); got.Port != 40000 {
		t.Fatalf("bound port drifted to %d", got.Port)
	}
}

func TestMalformedProbeDoesNotPinPort(t *testing.T) {
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{})

	fragmented := []byte{0, 0, 1, atypIPv4, 192, 0, 2, 1, 0, 53}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 41000), fragmented); !errors.Is(err, ErrFragmented) {
		t.Fatalf("FRAG error = %v, want ErrFragmented", err)
	}
	reserved := []byte{0, 1, 0, atypIPv4, 192, 0, 2, 1, 0, 53}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 41000), reserved); !errors.Is(err, ErrReservedNonZero) {
		t.Fatalf("RSV error = %v, want ErrReservedNonZero", err)
	}
	badDomain := []byte{0, 0, 0, atypDomain, 9, 'e', 'x', 'a', 'm'}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 41000), badDomain); !errors.Is(err, ErrInvalidDomainLength) {
		t.Fatalf("domain length error = %v, want ErrInvalidDomainLength", err)
	}
	if got := association.ClientAddr(); got.Port != 0 {
		t.Fatalf("invalid probes pinned port %d", got.Port)
	}

	valid := mustDatagram(t, Address{Host: "192.0.2.20", Port: 53}, "valid")
	if _, _, err := association.Admit(udpSource("127.0.0.1", 41001), valid); err != nil {
		t.Fatalf("valid datagram rejected: %v", err)
	}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 41000), valid); !errors.Is(err, ErrSourcePort) {
		t.Fatalf("old probe port error = %v, want ErrSourcePort", err)
	}
}

func TestDeclaredClientAddressAndPortAreEnforced(t *testing.T) {
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 42000,
	}, Policy{})
	packet := mustDatagram(t, Address{Host: "192.0.2.30", Port: 53}, "declared")

	if _, _, err := association.Admit(udpSource("127.0.0.1", 42001), packet); !errors.Is(err, ErrSourcePort) {
		t.Fatalf("unexpected source port error = %v, want ErrSourcePort", err)
	}
	if _, _, err := association.Admit(udpSource("127.0.0.2", 42000), packet); !errors.Is(err, ErrSourceAddress) {
		t.Fatalf("unexpected source address error = %v, want ErrSourceAddress", err)
	}
	if _, _, err := association.Admit(udpSource("127.0.0.1", 42000), packet); err != nil {
		t.Fatalf("declared client rejected: %v", err)
	}
}

func TestTargetPolicyRejectsForbiddenNetworksAndUncheckedDomains(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	_, private, _ := net.ParseCIDR("10.0.0.0/8")
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{
		Forbidden: []*net.IPNet{loopback, private},
		Resolver: func(_ context.Context, host string) ([]net.IP, error) {
			switch host {
			case "loopback.example":
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			case "public.example":
				return []net.IP{net.ParseIP("192.0.2.40")}, nil
			default:
				return nil, errors.New("unexpected host")
			}
		},
	})

	source := udpSource("127.0.0.1", 43000)
	tests := []struct {
		name   string
		target Address
		want   error
	}{
		{
			name:   "forbidden loopback literal",
			target: Address{Host: "127.0.0.1", Port: 53},
			want:   ErrTargetDenied,
		},
		{
			name:   "forbidden private literal",
			target: Address{Host: "10.1.2.3", Port: 53},
			want:   ErrTargetDenied,
		},
		{
			name:   "domain resolving to forbidden address",
			target: Address{Host: "loopback.example", Port: 53},
			want:   ErrTargetDenied,
		},
		{
			name:   "domain resolving to public address",
			target: Address{Host: "public.example", Port: 53},
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := association.Admit(source, mustDatagram(t, tc.target, "data"))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	noResolverControl, _ := tcpControlPair(t)
	noResolver := mustAssociation(t, noResolverControl, &net.UDPAddr{IP: net.IPv4zero}, Policy{
		Forbidden: []*net.IPNet{loopback},
	})
	if _, _, err := noResolver.Admit(source, mustDatagram(t, Address{Host: "example.com", Port: 53}, "data")); !errors.Is(err, ErrTargetUnresolvable) {
		t.Fatalf("unchecked domain error = %v, want ErrTargetUnresolvable", err)
	}
}

func TestControlConnectionCloseEndsAssociation(t *testing.T) {
	control, peer := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{})
	source := udpSource("127.0.0.1", 44000)
	packet := mustDatagram(t, Address{Host: "192.0.2.50", Port: 53}, "before")

	if _, _, err := association.Admit(source, packet); err != nil {
		t.Fatalf("admit before control close: %v", err)
	}
	if err := peer.Close(); err != nil {
		t.Fatalf("close TCP control peer: %v", err)
	}
	waitAssociationDone(t, association)

	if !errors.Is(association.Err(), ErrControlClosed) {
		t.Fatalf("association error = %v, want ErrControlClosed", association.Err())
	}
	if _, _, err := association.Admit(source, packet); !errors.Is(err, ErrAssociationClosed) {
		t.Fatalf("datagram after control close error = %v, want ErrAssociationClosed", err)
	}

	relay := &capturePacketConn{}
	if err := association.WriteReply(relay, Address{Host: "192.0.2.50", Port: 53}, []byte("after")); !errors.Is(err, ErrAssociationClosed) {
		t.Fatalf("reply after control close error = %v, want ErrAssociationClosed", err)
	}
}

func TestWriteReplyOnlyTargetsBoundClient(t *testing.T) {
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 45000,
	}, Policy{})

	relay := &capturePacketConn{}
	remote := Address{Host: "192.0.2.60", Port: 53}
	if err := association.WriteReply(relay, remote, []byte("reply")); err != nil {
		t.Fatalf("WriteReply: %v", err)
	}

	writes := relay.snapshot()
	if len(writes) != 1 {
		t.Fatalf("relay wrote %d datagrams, want 1", len(writes))
	}
	client, ok := writes[0].addr.(*net.UDPAddr)
	if !ok || client.IP.String() != "127.0.0.1" || client.Port != 45000 {
		t.Fatalf("reply destination = %v, want 127.0.0.1:45000", writes[0].addr)
	}

	target, payload, err := ParseDatagram(writes[0].packet)
	if err != nil {
		t.Fatalf("parse reply: %v", err)
	}
	if target != remote || string(payload) != "reply" {
		t.Fatalf("reply target/payload = %v/%q", target, payload)
	}
}

func TestWriteReplyRequiresPinnedClientPort(t *testing.T) {
	control, _ := tcpControlPair(t)
	association := mustAssociation(t, control, &net.UDPAddr{IP: net.IPv4zero}, Policy{})
	relay := &capturePacketConn{}
	if err := association.WriteReply(relay, Address{Host: "192.0.2.61", Port: 53}, []byte("reply")); !errors.Is(err, ErrNoClientPort) {
		t.Fatalf("error = %v, want ErrNoClientPort", err)
	}
}
