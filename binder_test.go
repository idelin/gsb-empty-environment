package taskpkg

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testHost = "api.example.com"

func testPolicy(t *testing.T, verified []string, forbidden []string, generation int64) *Policy {
	t.Helper()
	ips := make([]net.IP, 0, len(verified))
	for _, raw := range verified {
		ips = append(ips, net.ParseIP(raw))
	}
	policy, err := NewPolicy(testHost, 8443, ips, forbidden, generation)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func dialTarget(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}

func assertOnlyIPLiterals(t *testing.T, calls []dialCall) {
	t.Helper()
	for _, call := range calls {
		host, _, err := net.SplitHostPort(call.address)
		if err != nil {
			t.Fatalf("dial target %q is not host:port: %v", call.address, err)
		}
		if _, err := netip.ParseAddr(host); err != nil {
			t.Fatalf("dialer received host name %q instead of an IP literal", call.address)
		}
	}
}

func assertNoInternalPeer(t *testing.T, peers []net.Addr) {
	t.Helper()
	for _, peer := range peers {
		host, _, err := net.SplitHostPort(peer.String())
		if err != nil {
			t.Fatalf("callback peer %q: %v", peer, err)
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			t.Fatalf("callback peer %q is not an IP literal", peer)
		}
		if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
			t.Fatalf("internal peer %s was delivered", peer)
		}
	}
}

// TestDNSRebinding changes the resolver answer between an initial dial and a
// retry. The internal answer must never be dialed or delivered.
func TestDNSRebinding(t *testing.T) {
	resolver := &fakeResolver{}
	dialer := newFakeDialer()
	done := &delivered{}
	binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

	external := "203.0.113.10"
	internal := "10.0.0.5"
	policy := testPolicy(t, []string{external}, []string{"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16"}, 1)
	if err := binder.Update(policy); err != nil {
		t.Fatalf("Update: %v", err)
	}

	target := dialTarget(external, policy.Port)
	dialer.setConn(target, newFakeConn(external, policy.Port))
	resolver.setAnswers(net.ParseIP(external))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, peer, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	if conn == nil || peer == nil {
		t.Fatal("first Dial returned no connection or peer")
	}
	if peer.String() != target {
		t.Fatalf("callback peer = %q, want %q", peer.String(), target)
	}
	assertOnlyIPLiterals(t, dialer.dialCalls())
	assertNoInternalPeer(t, done.peers())

	// Attacker rebinds the name to an internal management address on retry.
	resolver.setAnswers(net.ParseIP(internal))
	_, _, err = binder.Dial(ctx, "tcp", testHost, policy.Port)
	if !errors.Is(err, ErrNoVerifiedEndpoint) {
		t.Fatalf("rebinding retry error = %v, want ErrNoVerifiedEndpoint", err)
	}

	for _, call := range dialer.dialCalls() {
		if strings.HasPrefix(call.address, internal+":") {
			t.Fatalf("internal answer was dialed: %s", call.address)
		}
	}
	assertNoInternalPeer(t, done.peers())

	for _, host := range resolver.queriedHosts() {
		if host != testHost {
			t.Fatalf("resolver queried unexpected host %q", host)
		}
	}
}

// TestSpoofedInternalPeer makes the underlying dial return a connection whose
// actual RemoteAddr is internal even though the dial target was a verified
// external literal. The gate must reject and close it.
func TestSpoofedInternalPeer(t *testing.T) {
	resolver := &fakeResolver{}
	dialer := newFakeDialer()
	done := &delivered{}
	binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

	good := "203.0.113.20"
	bad := "203.0.113.21"
	internal := "10.0.0.7"
	policy := testPolicy(t, []string{good, bad}, []string{"10.0.0.0/8"}, 1)
	if err := binder.Update(policy); err != nil {
		t.Fatalf("Update: %v", err)
	}

	dialer.setConn(dialTarget(good, policy.Port), newFakeConn(good, policy.Port))
	spoofed := newFakeConn(internal, policy.Port)
	dialer.setConn(dialTarget(bad, policy.Port), spoofed)
	resolver.setAnswers(net.ParseIP(good), net.ParseIP(bad))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, peer, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if peer.String() != dialTarget(good, policy.Port) {
		t.Fatalf("delivered peer = %q, want the external endpoint", peer)
	}
	if !spoofed.isClosed() {
		t.Fatal("spoofed internal connection was not closed")
	}
	_ = conn.Close()

	for _, deliveredPeer := range done.peers() {
		if strings.HasPrefix(deliveredPeer.String(), internal+":") {
			t.Fatalf("internal peer %s was delivered", deliveredPeer)
		}
	}
}

// TestIPv4MappedPeer exercises ::ffff:a.b.c.d peers: internal mapped peers
// are rejected, mapped verified peers are unwrapped and accepted, and a newly
// forbidden CIDR is enforced in mapped form.
func TestIPv4MappedPeer(t *testing.T) {
	t.Run("mapped internal peer rejected", func(t *testing.T) {
		resolver := &fakeResolver{}
		dialer := newFakeDialer()
		done := &delivered{}
		binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

		verified := "203.0.113.30"
		mappedInternal := "::ffff:10.0.0.9"
		policy := testPolicy(t, []string{verified}, []string{"10.0.0.0/8"}, 1)
		if err := binder.Update(policy); err != nil {
			t.Fatalf("Update: %v", err)
		}

		target := dialTarget(verified, policy.Port)
		dialer.setConn(target, newFakeConn(mappedInternal, policy.Port))
		resolver.setAnswers(net.ParseIP(verified))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
		if !errors.Is(err, ErrPeerNotVerified) {
			t.Fatalf("Dial error = %v, want ErrPeerNotVerified", err)
		}
		if done.count() != 0 {
			t.Fatalf("mapped internal peer was delivered %d times", done.count())
		}
		assertNoInternalPeer(t, done.peers())
	})

	t.Run("mapped verified peer accepted", func(t *testing.T) {
		resolver := &fakeResolver{}
		dialer := newFakeDialer()
		done := &delivered{}
		binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

		verified := "203.0.113.31"
		mappedVerified := "::ffff:203.0.113.31"
		policy := testPolicy(t, []string{verified}, nil, 1)
		if err := binder.Update(policy); err != nil {
			t.Fatalf("Update: %v", err)
		}

		conn := newFakeConn(mappedVerified, policy.Port)
		dialer.setConn(dialTarget(verified, policy.Port), conn)
		resolver.setAnswers(net.ParseIP(verified))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		deliveredConn, peer, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		if deliveredConn != conn {
			t.Fatal("delivered connection is not the mapped peer")
		}
		callbackPeer, ok := peer.(*net.TCPAddr)
		if !ok {
			t.Fatalf("callback peer type = %T, want *net.TCPAddr", peer)
		}
		// The *net.TCPAddr still carries the 4-in-6 byte form, while its
		// String method renders the equivalent IPv4 text.
		if !callbackPeer.IP.Equal(conn.remote.IP) {
			t.Fatalf("callback peer IP %v differs from connection peer %v", callbackPeer.IP, conn.remote.IP)
		}
		if got := callbackPeer.IP.String(); got != "203.0.113.31" {
			t.Fatalf("callback peer IP = %q, want canonical 203.0.113.31", got)
		}
		if peer.String() != net.JoinHostPort("203.0.113.31", strconv.Itoa(policy.Port)) {
			t.Fatalf("callback peer string = %q", peer.String())
		}
		_ = deliveredConn.Close()
	})

	t.Run("mapped peer hit by current forbidden CIDR", func(t *testing.T) {
		resolver := &fakeResolver{}
		dialer := newFakeDialer()
		done := &delivered{}
		binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

		verified := "198.51.100.40"
		mapped := "::ffff:198.51.100.40"
		policy := testPolicy(t, []string{verified}, []string{"198.51.100.0/24"}, 1)
		if err := binder.Update(policy); err != nil {
			t.Fatalf("Update: %v", err)
		}

		dialer.setConn(dialTarget(verified, policy.Port), newFakeConn(mapped, policy.Port))
		resolver.setAnswers(net.ParseIP(verified))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
		if !errors.Is(err, ErrPeerForbidden) {
			t.Fatalf("Dial error = %v, want ErrPeerForbidden", err)
		}
		if done.count() != 0 {
			t.Fatal("forbidden mapped peer reached the callback")
		}
	})
}

// TestDoubleSuccessRace starts two verified dials and releases both at once.
// Exactly one connection may be delivered and the loser must be closed.
func TestDoubleSuccessRace(t *testing.T) {
	resolver := &fakeResolver{}
	dialer := newFakeDialer()
	done := &delivered{}
	binder := NewBinder(Config{Resolver: resolver, Dialer: dialer, OnConnect: done.onConnect})

	first := "203.0.113.41"
	second := "203.0.113.42"
	policy := testPolicy(t, []string{first, second}, nil, 1)
	if err := binder.Update(policy); err != nil {
		t.Fatalf("Update: %v", err)
	}

	firstConn := newFakeConn(first, policy.Port)
	secondConn := newFakeConn(second, policy.Port)
	dialer.setConn(dialTarget(first, policy.Port), firstConn)
	dialer.setConn(dialTarget(second, policy.Port), secondConn)

	firstEnter, firstRelease := dialer.gate(dialTarget(first, policy.Port))
	secondEnter, secondRelease := dialer.gate(dialTarget(second, policy.Port))

	resolver.setAnswers(net.ParseIP(first), net.ParseIP(second))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type dialResult struct {
		conn net.Conn
		peer net.Addr
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		conn, peer, err := binder.Dial(ctx, "tcp", testHost, policy.Port)
		resultCh <- dialResult{conn: conn, peer: peer, err: err}
	}()

	<-firstEnter
	<-secondEnter
	close(firstRelease)
	close(secondRelease)

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Dial: %v", result.err)
	}

	if done.count() != 1 {
		t.Fatalf("callback fired %d times, want exactly 1", done.count())
	}
	if firstConn.isClosed() == secondConn.isClosed() {
		t.Fatal("exactly one of the two parallel connections must remain open")
	}

	deliveredPeer := done.peers()[0].String()
	if deliveredPeer != result.peer.String() {
		t.Fatalf("callback peer %q differs from returned peer %q", deliveredPeer, result.peer)
	}
	assertOnlyIPLiterals(t, dialer.dialCalls())
	_ = result.conn.Close()
}
