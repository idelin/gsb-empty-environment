package configlease

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

const ttl = 10 * time.Second

func newCluster() (*ManualClock, *Center) {
	clock := NewManualClock(t0)
	center := NewCenter(clock, ttl, Config{Version: 1, DBPassword: "s3cret-v1"})
	return clock, center
}

// A node holding a valid lease serves traffic with the leased config.
func TestNodeServesWithValidLease(t *testing.T) {
	_, center := newCluster()
	node := NewNode("n1", center, center.clock)

	out, err := node.Serve("SELECT 1")
	if err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	if !strings.Contains(out, "version=1") || !strings.Contains(out, "dbpassword=s3cret-v1") {
		t.Fatalf("unexpected response: %s", out)
	}
	if !center.Validate(node.Lease().ID) {
		t.Fatal("center should validate the node's lease")
	}
}

// Acceptance boundary 1+2: the center changes the DB password, publishes the
// new config, and revokes all old leases; nodes re-bind to the new version.
func TestConfigUpdateRevokesOldLeases(t *testing.T) {
	_, center := newCluster()
	n1 := NewNode("n1", center, center.clock)
	n2 := NewNode("n2", center, center.clock)
	oldLease1 := n1.Lease()
	oldLease2 := n2.Lease()

	// Config center modifies the DB password and publishes the new config.
	center.UpdateConfig(Config{DBPassword: "s3cret-v2"})

	// Old lease credentials are revoked immediately.
	if center.Validate(oldLease1.ID) || center.Validate(oldLease2.ID) {
		t.Fatal("old leases must be revoked after config update")
	}

	// Nodes renew: the center hands them the new config and new leases.
	n1.Renew()
	n2.Renew()

	for i, n := range []*Node{n1, n2} {
		cfg := n.Config()
		if cfg.Version != 2 || cfg.DBPassword != "s3cret-v2" {
			t.Fatalf("node %d bound to stale config: %+v", i, cfg)
		}
		if n.Lease().ID == oldLease1.ID || n.Lease().ID == oldLease2.ID {
			t.Fatalf("node %d still holds a revoked lease credential", i)
		}
		if !center.Validate(n.Lease().ID) {
			t.Fatalf("node %d new lease must validate", i)
		}
		out, err := n.Serve("SELECT 1")
		if err != nil || !strings.Contains(out, "dbpassword=s3cret-v2") {
			t.Fatalf("node %d must serve with new password, got %q, %v", i, out, err)
		}
	}
}

// Acceptance boundary 3: a partitioned slow node cannot renew; its old lease
// expires naturally and it stops accepting traffic on its own, instead of
// touching data with the stale password.
func TestDisconnectedNodeFencesItselfOnLeaseExpiry(t *testing.T) {
	clock, center := newCluster()
	fast := NewNode("fast", center, clock)
	slow := NewNode("slow", center, clock)

	// Center updates the password and revokes old leases. The slow node is
	// offline and never learns about it.
	center.UpdateConfig(Config{DBPassword: "s3cret-v2"})

	// Fast node stays connected: keeps renewing, moves to v2.
	clock.Advance(5 * time.Second)
	fast.Renew()
	if fast.Config().DBPassword != "s3cret-v2" {
		t.Fatal("fast node should have picked up the new password")
	}

	// Slow node is still within its old lease TTL: it keeps serving with the
	// old config (lease not yet expired, node cannot know better).
	out, err := slow.Serve("UPDATE accounts SET balance = balance - 1")
	if err != nil || !strings.Contains(out, "dbpassword=s3cret-v1") {
		t.Fatalf("slow node should serve under old lease, got %q, %v", out, err)
	}

	// Old lease expires naturally while the node is still disconnected.
	clock.Advance(6 * time.Second) // t=11s > 10s TTL

	if _, err := slow.Serve("UPDATE accounts SET balance = balance - 1"); err != ErrLeaseExpired {
		t.Fatalf("slow node must fence itself after lease expiry, got %v", err)
	}
	// It stays fenced until it can re-bind.
	if _, err := slow.Serve("SELECT 1"); err != ErrLeaseExpired {
		t.Fatalf("slow node must keep refusing traffic, got %v", err)
	}

	// Once connectivity returns it re-binds to the current version and
	// resumes serving with the new password.
	slow.Renew()
	out, err = slow.Serve("SELECT 1")
	if err != nil || !strings.Contains(out, "version=2 dbpassword=s3cret-v2") {
		t.Fatalf("reconnected slow node should serve v2, got %q, %v", out, err)
	}
}

// Renew extends the same lease credential while the config is unchanged.
func TestRenewExtendsLease(t *testing.T) {
	clock, center := newCluster()
	node := NewNode("n1", center, clock)
	first := node.Lease()

	clock.Advance(6 * time.Second)
	node.Renew()
	second := node.Lease()

	if second.ID != first.ID {
		t.Fatalf("renew should extend the same lease, got %s then %s", first.ID, second.ID)
	}
	if !second.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("renew should push expiry out: %v -> %v", first.ExpiresAt, second.ExpiresAt)
	}
	if _, err := node.Serve("SELECT 1"); err != nil {
		t.Fatalf("node should still serve after renew: %v", err)
	}
}

// A node that lets its lease lapse cannot be validated at the center either.
func TestCenterRejectsExpiredLease(t *testing.T) {
	clock, center := newCluster()
	node := NewNode("n1", center, clock)
	id := node.Lease().ID

	clock.Advance(ttl + time.Second)
	if center.Validate(id) {
		t.Fatal("center must reject an expired lease")
	}
}
