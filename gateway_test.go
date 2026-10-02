package main

import (
	"strings"
	"testing"
	"time"
)

// newSplitGateway builds a gateway whose [A-Z) range was leased to Node-1
// and then split at "M" into [A-M) on Node-1 and [M-Z) on Node-2.
func newSplitGateway(t *testing.T) (*Gateway, *Node, *Node) {
	t.Helper()
	g := NewGateway()
	node1 := &Node{ID: "Node-1"}
	node2 := &Node{ID: "Node-2"}
	g.AddNode(node1)
	g.AddNode(node2)
	if _, err := g.GrantLease(KeyRange{Start: "A", End: maxBound}, "Node-1"); err != nil {
		t.Fatalf("grant initial lease: %v", err)
	}
	if _, _, err := g.Split("M", "Node-1", "Node-2"); err != nil {
		t.Fatalf("split: %v", err)
	}
	return g, node1, node2
}

func TestSplitProducesIndependentChildLeases(t *testing.T) {
	g := NewGateway()
	g.AddNode(&Node{ID: "Node-1"})
	g.AddNode(&Node{ID: "Node-2"})

	parent, err := g.GrantLease(KeyRange{Start: "A", End: maxBound}, "Node-1")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	left, right, err := g.Split("M", "Node-1", "Node-2")
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	// Parent lease is revoked by the split.
	if parent.Valid(time.Now()) {
		t.Fatal("parent lease should be revoked after split")
	}

	// Children cover [A-M) and [M-Z), contiguous with no gap/overlap.
	if left.Range.Start != "A" || left.Range.End != "M" {
		t.Fatalf("left range = %s, want [A-M)", left.Range)
	}
	if right.Range.Start != "M" || right.Range.End != maxBound {
		t.Fatalf("right range = %s, want [M-Z)", right.Range)
	}

	// Independent holders.
	if left.NodeID != "Node-1" || right.NodeID != "Node-2" {
		t.Fatalf("holders = %s/%s, want Node-1/Node-2", left.NodeID, right.NodeID)
	}

	// Lease inheritance: children inherit parent epoch+1 and TTL.
	if left.Epoch != parent.Epoch+1 || right.Epoch != parent.Epoch+1 {
		t.Fatalf("child epochs = %d/%d, want parent epoch+1 = %d",
			left.Epoch, right.Epoch, parent.Epoch+1)
	}
	if left.TTL != parent.TTL || right.TTL != parent.TTL {
		t.Fatal("children should inherit the parent TTL")
	}
}

func TestRoutingAfterSplit(t *testing.T) {
	g, _, _ := newSplitGateway(t)

	cases := []struct {
		key  string
		want string
	}{
		{"Apple", "Node-1"},
		{"Lemon", "Node-1"},
		{"Mango", "Node-2"}, // boundary key "M..." belongs to the right range
		{"Zoo", "Node-2"},
	}
	for _, c := range cases {
		lease, node, err := g.Route(c.key)
		if err != nil {
			t.Fatalf("route %q: %v", c.key, err)
		}
		if node.ID != c.want {
			t.Errorf("route %q -> %s (%s), want %s", c.key, node.ID, lease.Range, c.want)
		}
	}

	if _, _, err := g.Route("0outside"); err == nil {
		t.Error("routing a key outside every lease should fail")
	}
}

func TestConcurrentQueriesOnDifferentNodesDoNotBlock(t *testing.T) {
	g, node1, node2 := newSplitGateway(t)

	// Node-1 blocks inside its query handler until released.
	started := make(chan struct{})
	release := make(chan struct{})
	node1.SetQueryHook(func(string) {
		close(started)
		<-release
	})

	appleDone := make(chan error, 1)
	go func() {
		_, err := g.Query("Apple") // routes to Node-1, blocks there
		appleDone <- err
	}()
	<-started // Node-1 is now blocked processing Apple

	zooDone := make(chan error, 1)
	go func() {
		_, err := g.Query("Zoo") // routes to Node-2
		zooDone <- err
	}()

	// Zoo must complete even though Apple is still blocked on Node-1.
	select {
	case err := <-zooDone:
		if err != nil {
			t.Fatalf("Zoo query: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Zoo query blocked behind Apple query on another node")
	}

	// Apple is still blocked.
	select {
	case <-appleDone:
		t.Fatal("Apple query finished before Node-1 was released")
	default:
	}

	close(release)
	if err := <-appleDone; err != nil {
		t.Fatalf("Apple query: %v", err)
	}

	if got := node1.Processed(); got != 1 {
		t.Errorf("Node-1 processed %d queries, want 1", got)
	}
	if got := node2.Processed(); got != 1 {
		t.Errorf("Node-2 processed %d queries, want 1", got)
	}
}

func TestGrantLeaseRejectsOverlap(t *testing.T) {
	g := NewGateway()
	g.AddNode(&Node{ID: "Node-1"})
	g.AddNode(&Node{ID: "Node-2"})

	if _, err := g.GrantLease(KeyRange{Start: "A", End: "M"}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := g.GrantLease(KeyRange{Start: "K", End: "Z"}, "Node-2"); err == nil {
		t.Fatal("overlapping grant should fail")
	}
	if _, err := g.GrantLease(KeyRange{Start: "M", End: "Z"}, "Node-2"); err != nil {
		t.Fatalf("adjacent (non-overlapping) grant should succeed: %v", err)
	}
}

func TestSplitErrors(t *testing.T) {
	g := NewGateway()
	g.AddNode(&Node{ID: "Node-1"})
	g.AddNode(&Node{ID: "Node-2"})
	if _, err := g.GrantLease(KeyRange{Start: "A", End: maxBound}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, _, err := g.Split("M", "Node-1", "ghost"); err == nil {
		t.Error("split with unknown node should fail")
	}
	if _, _, err := g.Split("A", "Node-1", "Node-2"); err == nil {
		t.Error("split at the range start should fail")
	}
	if _, _, err := g.Split("0", "Node-1", "Node-2"); err == nil {
		t.Error("split outside any lease should fail")
	}
	// Failed splits must not disturb the existing lease.
	if _, node, err := g.Route("Apple"); err != nil || node.ID != "Node-1" {
		t.Errorf("routing after failed splits = %v/%v, want Node-1", node, err)
	}
}

func TestLeaseExpiry(t *testing.T) {
	g := NewGateway()
	g.LeaseTTL = 30 * time.Millisecond
	g.AddNode(&Node{ID: "Node-1"})
	if _, err := g.GrantLease(KeyRange{Start: "A", End: maxBound}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, err := g.Query("Apple"); err != nil {
		t.Fatalf("query before expiry: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	_, err := g.Query("Apple")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("query after expiry err = %v, want expiry error", err)
	}
}
