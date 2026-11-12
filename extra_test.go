package netsec

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func errorIs(got, want error) bool { return errors.Is(got, want) }

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for operation result")
		return nil
	}
}

func waitAttached(t *testing.T, ch <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case conn := <-ch:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Attach")
		return nil
	}
}

// waitActiveConn polls the transfer until its active dial has delivered the
// single connection, then returns it.
func waitActiveConn(t *testing.T, transfer *Transfer) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if transfer.State() == Attached {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			conn, err := transfer.Attach(ctx)
			cancel()
			if err == nil && conn != nil {
				return conn
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("active transfer never reached Attached")
	return nil
}

func eventuallyQueueLen(l *PausableListener, want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		n := len(l.queue)
		l.mu.Unlock()
		if n == want {
			return n
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

func mustListen(t *testing.T, c *PausableConnector, addr netip.AddrPort) *PausableListener {
	t.Helper()
	ln, _, err := c.Listen(context.Background(), addr)
	if err != nil {
		t.Fatalf("Listen(%s): %v", addr, err)
	}
	return ln.(*PausableListener)
}

func acceptOne(t *testing.T, ln *PausableListener) net.Conn {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return conn
}

func expectEcho(t *testing.T, client, server net.Conn) {
	t.Helper()
	payload := []byte("channel-binding-payload")
	writeDone := make(chan struct{})
	go func() {
		_, _ = server.Write(payload)
		close(writeDone)
	}()
	readExact(t, client, payload)
	<-writeDone
}

// TestApprovalBinding verifies each approval field is pinned and re-checked.
func TestApprovalBinding(t *testing.T) {
	session, _ := mustSession(t, "erin")

	bad := []Approval{
		{Account: "erin", Mode: Passive, AllowedPeers: []netip.AddrPort{goodPeer}},
		{Digest: DigestOf([]byte("x")), Mode: Passive, AllowedPeers: []netip.AddrPort{goodPeer}},
		{Digest: DigestOf([]byte("x")), Account: "erin", AllowedPeers: []netip.AddrPort{goodPeer}},
		{Digest: DigestOf([]byte("x")), Account: "erin", Mode: Passive},
		{Digest: DigestOf([]byte("x")), Account: "mallory", Mode: Passive, AllowedPeers: []netip.AddrPort{goodPeer}},
	}
	for i, approval := range bad {
		if _, err := session.Authorize(approval); !errors.Is(err, ErrInvalidApproval) {
			t.Fatalf("case %d: err = %v, want ErrInvalidApproval", i, err)
		}
	}

	passive, err := session.Authorize(passiveApproval("erin", goodPeer))
	if err != nil {
		t.Fatalf("Authorize passive: %v", err)
	}
	if err := passive.ConnectActive(context.Background(), goodPeer); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("active open on passive approval: %v", err)
	}

	active, err := session.Authorize(activeApproval("erin", goodPeer))
	if err != nil {
		t.Fatalf("Authorize active: %v", err)
	}
	if _, err := active.OpenPassive(context.Background()); !errors.Is(err, ErrModeMismatch) {
		t.Fatalf("passive open on active approval: %v", err)
	}
}

// TestOneShotApproval verifies an approval never serves two transfers and
// digest/account/peer changes produce distinct authorizations.
func TestOneShotApproval(t *testing.T) {
	session, connector := mustSession(t, "frank")
	approval := passiveApproval("frank", goodPeer)

	first, err := session.Authorize(approval)
	if err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	bound, err := first.OpenPassive(context.Background())
	if err != nil {
		t.Fatalf("OpenPassive: %v", err)
	}

	// "Reusing" the same approval value is impossible because instances are
	// distinct and the first cannot be re-opened.
	if _, err := first.OpenPassive(context.Background()); !errors.Is(err, ErrTransferInUse) {
		t.Fatalf("reopen instance: %v", err)
	}

	listener, _ := connector.Listener(bound)
	legit := listener.Inject(goodPeer)
	dataConn := assertAttaches(t, first)
	expectEcho(t, dataConn, legit)
	_ = dataConn.Close()
	_ = first.Complete()

	// The next transfer requires a new authorization and gets a new endpoint.
	second, err := session.Authorize(approval)
	if err != nil {
		t.Fatalf("second Authorize: %v", err)
	}
	if second.ID() == first.ID() {
		t.Fatal("new transfer reused the old instance identity")
	}
	secondBound, err := second.OpenPassive(context.Background())
	if err != nil {
		t.Fatalf("second OpenPassive: %v", err)
	}
	if secondBound == bound {
		t.Fatal("second transfer reused the first passive endpoint")
	}

	// A different digest must not attach to the second instance.
	differentDigest := Approval{
		Digest:       DigestOf([]byte("DELE /different/operation")),
		Account:      "frank",
		Mode:         Passive,
		AllowedPeers: []netip.AddrPort{otherPeer},
	}
	other, err := session.Authorize(differentDigest)
	if err != nil {
		t.Fatalf("other Authorize: %v", err)
	}
	if other.Digest() == second.Digest() {
		t.Fatal("distinct operations produced identical digests")
	}
	if other.ID() == second.ID() {
		t.Fatal("distinct approvals shared one transfer instance")
	}
	if err := other.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	_ = second.Cancel()
}

// TestTCPConnectorLoopback exercises the real socket connector end to end.
func TestTCPConnectorLoopback(t *testing.T) {
	session, err := NewSession("gina", TCPConnector{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// Stand up the approved active target on a real loopback listener.
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Skipf("sandbox forbids real TCP bind: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	target, err := netip.ParseAddrPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	transfer, err := session.Authorize(activeApproval("gina", target))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		_, writeErr := conn.Write([]byte("loopback body"))
		_ = conn.Close()
		serverErr <- writeErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transfer.ConnectActive(ctx, target); err != nil {
		t.Fatalf("ConnectActive: %v", err)
	}
	dataConn, err := transfer.Attach(ctx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	readExact(t, dataConn, []byte("loopback body"))
	_ = dataConn.Close()
	if err := <-serverErr; err != nil {
		t.Fatalf("server: %v", err)
	}
	if err := transfer.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}
