package netsec

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

const testCID uint32 = 42

func pipePair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		server.Close()
	})
	return client, server
}

// isClosed reports that a connection's read side has been closed.
func isClosed(t *testing.T, conn net.Conn) bool {
	t.Helper()
	// A closed pipe rejects even deadline configuration with ErrClosedPipe;
	// ignore that and let the Read below report the terminal state.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err == nil {
		return false
	}
	// An open pipe with no buffered data only times out; a closed peer reports
	// EOF or ErrClosed.
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}

func TestReservedCIDRejected(t *testing.T) {
	registry := NewRegistry()

	for cid := ReservedCIDMin; cid <= ReservedCIDMax; cid++ {
		if _, err := registry.Register(cid, "vm-reserved", nil); !errors.Is(err, ErrReservedCID) {
			t.Fatalf("Register(cid=%d) error = %v, want ErrReservedCID", cid, err)
		}

		client, server := pipePair(t)
		bound, err := registry.Bind(cid, 1024, server)
		if !errors.Is(err, ErrReservedCID) {
			t.Fatalf("Bind(cid=%d) error = %v, want ErrReservedCID", cid, err)
		}
		if bound != nil {
			t.Fatalf("Bind(cid=%d) returned a connection for a reserved CID", cid)
		}
		// Rejected connections must be closed by the registry, unblocking peer.
		if !isClosed(t, client) {
			t.Fatalf("rejected conn for reserved cid %d was not closed", cid)
		}
		client.Close()
	}

	if !IsReservedCID(0) || !IsReservedCID(1) || !IsReservedCID(2) || IsReservedCID(3) {
		t.Fatal("IsReservedCID classifies CIDs incorrectly")
	}
}

func TestPortAuthorization(t *testing.T) {
	registry := NewRegistry()
	const allowed = uint32(8080)
	if _, err := registry.Register(testCID, "vm-a", []uint32{allowed, 9090}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	client, server := pipePair(t)
	bound, err := registry.Bind(testCID, allowed, server)
	if err != nil {
		t.Fatalf("Bind allowed port: %v", err)
	}
	if bound.CID() != testCID || bound.Port() != allowed || bound.InstanceID() != "vm-a" {
		t.Fatalf("bound conn metadata = cid %d port %d instance %q", bound.CID(), bound.Port(), bound.InstanceID())
	}
	if registry.ActiveConnections(testCID) != 1 {
		t.Fatal("allowed connection was not tracked")
	}

	// An allowlisted connection carries real traffic before revocation.
	go func() {
		_, _ = client.Write([]byte("ping"))
	}()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(bound, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("bound conn read = %q, %v", buf, err)
	}

	otherClient, otherServer := pipePair(t)
	if _, err := registry.Bind(testCID, 22, otherServer); !errors.Is(err, ErrPortNotAllowed) {
		t.Fatalf("Bind disallowed port error = %v, want ErrPortNotAllowed", err)
	}
	if !isClosed(t, otherClient) {
		t.Fatal("over-privileged connection was not closed")
	}
	otherClient.Close()
	if registry.ActiveConnections(testCID) != 1 {
		t.Fatal("rejected connection must not be tracked")
	}

	// Explicit close removes the connection from the generation.
	if err := bound.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if registry.ActiveConnections(testCID) != 0 {
		t.Fatal("explicit Close did not untrack the connection")
	}
	if err := bound.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestCIDReuseClosesOldGenerationAndStaleCallbacks(t *testing.T) {
	registry := NewRegistry()

	attackerToken, err := registry.Register(testCID, "attacker-vm", []uint32{1001})
	if err != nil {
		t.Fatalf("attacker Register: %v", err)
	}
	if attackerToken.Generation() != 1 {
		t.Fatalf("first generation = %d, want 1", attackerToken.Generation())
	}

	attackerClient, attackerServer := pipePair(t)
	if _, err := registry.Bind(testCID, 1001, attackerServer); err != nil {
		t.Fatalf("attacker Bind: %v", err)
	}

	// The attacker VM is destroyed and the same CID is handed to a new tenant.
	tenantToken, err := registry.Register(testCID, "tenant-vm", []uint32{2002})
	if err != nil {
		t.Fatalf("tenant Register: %v", err)
	}
	if tenantToken.Generation() != 2 {
		t.Fatalf("reused CID generation = %d, want 2", tenantToken.Generation())
	}

	if !isClosed(t, attackerClient) {
		t.Fatal("attacker connection survived CID reassignment")
	}
	attackerClient.Close()
	if registry.ActiveConnections(testCID) != 0 {
		t.Fatal("old generation connections were not cleaned up on reuse")
	}

	// The new tenant only gets its own port set; the attacker's old port is gone.
	{
		_, server := pipePair(t)
		if _, err := registry.Bind(testCID, 1001, server); !errors.Is(err, ErrPortNotAllowed) {
			t.Fatalf("attacker port reachable after reuse: %v", err)
		}
	}

	tenantClient, tenantServer := pipePair(t)
	tenantConn, err := registry.Bind(testCID, 2002, tenantServer)
	if err != nil {
		t.Fatalf("tenant Bind: %v", err)
	}
	if tenantConn.InstanceID() != "tenant-vm" || tenantConn.Generation() != 2 {
		t.Fatalf("tenant conn = instance %q gen %d", tenantConn.InstanceID(), tenantConn.Generation())
	}

	// A late Accept callback still carrying the attacker's generation token is
	// discarded even though the CID is live again under the new tenant.
	staleClient, staleServer := pipePair(t)
	if _, err := registry.BindToken(attackerToken, 1001, staleServer); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale token BindToken error = %v, want ErrStaleGeneration", err)
	}
	if !isClosed(t, staleClient) {
		t.Fatal("stale-generation callback connection was not closed")
	}
	staleClient.Close()

	// A callback with the current token and an allowlisted port is accepted.
	_, currentServer := pipePair(t)
	currentConn, err := registry.BindToken(tenantToken, 2002, currentServer)
	if err != nil {
		t.Fatalf("current token BindToken: %v", err)
	}
	defer currentConn.Close()
	if currentConn.Generation() != 2 {
		t.Fatalf("current token bound to generation %d, want 2", currentConn.Generation())
	}

	// After the tenant instance is destroyed, plain Bind and token Bind both fail.
	registry.Revoke(testCID)
	if !isClosed(t, tenantClient) {
		t.Fatal("tenant connection survived Revoke")
	}
	tenantClient.Close()
	{
		_, server := pipePair(t)
		if _, err := registry.Bind(testCID, 2002, server); !errors.Is(err, ErrUnknownCID) {
			t.Fatalf("Bind after revoke error = %v, want ErrUnknownCID", err)
		}
	}
	{
		_, server := pipePair(t)
		if _, err := registry.BindToken(tenantToken, 2002, server); !errors.Is(err, ErrStaleGeneration) {
			t.Fatalf("BindToken after revoke error = %v, want ErrStaleGeneration", err)
		}
	}
}

func TestRevokeUnknownCIDIsNoop(t *testing.T) {
	registry := NewRegistry()
	registry.Revoke(999) // must not panic or affect other CIDs

	if _, err := registry.Register(testCID, "vm-a", []uint32{7}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, server := pipePair(t)
	bound, err := registry.Bind(testCID, 7, server)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	registry.Revoke(999)
	if registry.ActiveConnections(testCID) != 1 {
		t.Fatal("unrelated revoke disturbed another CID's generation")
	}
	if isClosed(t, client) {
		t.Fatal("connection closed by an unrelated CID revoke")
	}
	bound.Close()
}

func TestConcurrentBindAndDestroy(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Register(testCID, "vm-race", []uint32{5000}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const workers = 16
	const iterations = 200

	var (
		mu      sync.Mutex
		bound   []*BoundConn
		wg      sync.WaitGroup
		stopMu  sync.Mutex
		stopped bool
	)

	isStopped := func() bool {
		stopMu.Lock()
		defer stopMu.Unlock()
		return stopped
	}

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if isStopped() {
					return
				}
				client, server := net.Pipe()
				conn, err := registry.Bind(testCID, 5000, server)
				if err != nil {
					// Rejected connections are closed for us; close the peer too.
					client.Close()
					continue
				}
				mu.Lock()
				bound = append(bound, conn)
				mu.Unlock()
			}
		}()
	}

	// Continuously destroy and re-create the instance behind the same CID.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if isStopped() {
				return
			}
			registry.Revoke(testCID)
			if _, err := registry.Register(testCID, "vm-race", []uint32{5000}); err != nil {
				stopMu.Lock()
				stopped = true
				stopMu.Unlock()
				t.Errorf("re-register: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	// Final destruction: every connection ever handed out must be closed and
	// no entry may remain tracked.
	registry.Revoke(testCID)

	mu.Lock()
	snapshot := append([]*BoundConn(nil), bound...)
	mu.Unlock()

	if got := registry.ActiveConnections(testCID); got != 0 {
		t.Fatalf("ActiveConnections after final revoke = %d, want 0", got)
	}
	for i, conn := range snapshot {
		if !isClosed(t, conn) {
			t.Fatalf("connection %d/%d survived concurrent destruction", i, len(snapshot))
		}
		conn.Close()
	}
}
