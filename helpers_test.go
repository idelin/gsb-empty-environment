package netsec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

var (
	goodPeer   = netip.MustParseAddrPort("127.0.0.1:5001")
	evilPeer   = netip.MustParseAddrPort("127.0.0.2:6000")
	otherPeer  = netip.MustParseAddrPort("127.0.0.3:6001")
	waitBudget = 2 * time.Second
)

func mustSession(t *testing.T, account string) (*Session, *PausableConnector) {
	t.Helper()
	conn := NewPausableConnector()
	session, err := NewSession(account, conn)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, conn
}

func passiveApproval(account string, peers ...netip.AddrPort) Approval {
	return Approval{
		Digest:       DigestOf([]byte("RETR /data/" + account)),
		Account:      account,
		Mode:         Passive,
		AllowedPeers: peers,
	}
}

func activeApproval(account string, peers ...netip.AddrPort) Approval {
	return Approval{
		Digest:       DigestOf([]byte("STOR /inbox/" + account)),
		Account:      account,
		Mode:         Active,
		AllowedPeers: peers,
	}
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// assertZeroBody verifies conn delivers no data and is closed by the proxy.
// net.Pipe surfaces a half-closed peer as immediate EOF on read and a closed
// peer as write failure, both of which prove zero bytes were exchanged.
func assertZeroBody(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))

	buf := make([]byte, 64)
	n, readErr := conn.Read(buf)
	if n != 0 {
		t.Fatalf("unauthorized peer read %d bytes, want zero body", n)
	}
	if readErr == nil {
		t.Fatal("unauthorized peer read returned cleanly, want block or close")
	}

	if _, writeErr := conn.Write([]byte("x")); writeErr == nil {
		t.Fatal("unauthorized peer write succeeded, want closed connection")
	}
}

func assertAttaches(t *testing.T, transfer *Transfer) net.Conn {
	t.Helper()
	attachCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := transfer.Attach(attachCtx)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if conn == nil {
		t.Fatal("Attach returned nil connection")
	}
	return conn
}

// readExact reads len(want) bytes, failing on short reads or timeouts.
func readExact(t *testing.T, conn net.Conn, want []byte) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
