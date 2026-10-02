package leaserouter

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// newSplitGateway builds the acceptance-test topology:
// the full key space [A, ∞) starts on Node-1, then splits at 'M' so
// [A, M) stays with Node-1 and [M, ∞) is granted to Node-2.
func newSplitGateway(t *testing.T) (*Gateway, *Router, *Node, *Node) {
	t.Helper()
	r := NewRouter(time.Minute)
	n1 := NewNode("Node-1", 0)
	n2 := NewNode("Node-2", 0)
	r.AddNode(n1)
	r.AddNode(n2)
	n1.Put("Apple", "apple-value-from-Node-1")
	n2.Put("Zoo", "zoo-value-from-Node-2")

	if _, err := r.Grant(KeyRange{Start: "A"}, "Node-1"); err != nil {
		t.Fatalf("grant root range: %v", err)
	}
	if _, _, err := r.Split("M", "Node-2"); err != nil {
		t.Fatalf("split at M: %v", err)
	}
	return NewGateway(r), r, n1, n2
}

func TestRouteBeforeSplit(t *testing.T) {
	r := NewRouter(time.Minute)
	n1 := NewNode("Node-1", 0)
	r.AddNode(n1)
	if _, err := r.Grant(KeyRange{Start: "A"}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	for _, key := range []string{"Apple", "Zoo"} {
		lease, node, err := r.Route(key)
		if err != nil {
			t.Fatalf("route %q: %v", key, err)
		}
		if node != n1 || lease.NodeID != "Node-1" {
			t.Fatalf("key %q routed to %s, want Node-1", key, lease.NodeID)
		}
	}
}

func TestSplitCreatesIndependentLeasesWithInheritance(t *testing.T) {
	r := NewRouter(time.Minute)
	n1 := NewNode("Node-1", 0)
	n2 := NewNode("Node-2", 0)
	r.AddNode(n1)
	r.AddNode(n2)

	root, err := r.Grant(KeyRange{Start: "A"}, "Node-1")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	left, right, err := r.Split("M", "Node-2")
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	if left.Range != (KeyRange{Start: "A", End: "M"}) {
		t.Fatalf("left range = %s, want [A, M)", left.Range)
	}
	if right.Range != (KeyRange{Start: "M", End: ""}) {
		t.Fatalf("right range = %s, want [M, ∞)", right.Range)
	}
	if left.NodeID != "Node-1" || right.NodeID != "Node-2" {
		t.Fatalf("holders = %s/%s, want Node-1/Node-2", left.NodeID, right.NodeID)
	}
	if left.ID == right.ID {
		t.Fatal("children must have independent lease IDs")
	}
	if left.Parent != root.ID || right.Parent != root.ID {
		t.Fatalf("children must record parent lease #%d, got #%d/#%d", root.ID, left.Parent, right.Parent)
	}
	if left.Epoch != root.Epoch+1 || right.Epoch != root.Epoch+1 {
		t.Fatalf("children must inherit bumped epoch %d, got %d/%d", root.Epoch+1, left.Epoch, right.Epoch)
	}
	if got := len(r.Leases()); got != 2 {
		t.Fatalf("lease table size = %d, want 2", got)
	}
}

func TestRoutingAfterSplit(t *testing.T) {
	gw, r, _, _ := newSplitGateway(t)

	cases := []struct {
		key  string
		node string
	}{
		{"Apple", "Node-1"},
		{"A", "Node-1"},
		{"Lemon", "Node-1"},
		{"M", "Node-2"}, // boundary belongs to the right half
		{"Zoo", "Node-2"},
	}
	for _, c := range cases {
		lease, _, err := r.Route(c.key)
		if err != nil {
			t.Fatalf("route %q: %v", c.key, err)
		}
		if lease.NodeID != c.node {
			t.Fatalf("key %q -> %s, want %s", c.key, lease.NodeID, c.node)
		}
	}

	if v, err := gw.Query("Apple"); err != nil || v != "apple-value-from-Node-1" {
		t.Fatalf("query Apple = %q, %v", v, err)
	}
	if v, err := gw.Query("Zoo"); err != nil || v != "zoo-value-from-Node-2" {
		t.Fatalf("query Zoo = %q, %v", v, err)
	}
}

// TestConcurrentQueriesDoNotBlock proves that a stalled Node-1 cannot
// block a query served by Node-2: Node-1's lock is held externally while
// "Zoo" is queried, and the query must still complete.
func TestConcurrentQueriesDoNotBlock(t *testing.T) {
	gw, _, n1, _ := newSplitGateway(t)

	n1.mu.Lock() // simulate Node-1 being stuck on a long request
	zooDone := make(chan error, 1)
	go func() {
		v, err := gw.Query("Zoo")
		if err == nil && v != "zoo-value-from-Node-2" {
			err = errUnexpected(v)
		}
		zooDone <- err
	}()
	select {
	case err := <-zooDone:
		if err != nil {
			t.Fatalf("query Zoo: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("query Zoo blocked behind stalled Node-1")
	}

	appleDone := make(chan struct{})
	go func() {
		_, _ = gw.Query("Apple")
		close(appleDone)
	}()
	select {
	case <-appleDone:
		t.Fatal("query Apple should block while Node-1 is stalled")
	case <-time.After(100 * time.Millisecond):
		// still blocked on Node-1's lock, as expected
	}
	n1.mu.Unlock()
	select {
	case <-appleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("query Apple did not complete after Node-1 recovered")
	}
}

func errUnexpected(v string) error {
	return &unexpectedError{v}
}

type unexpectedError struct{ v string }

func (e *unexpectedError) Error() string { return "unexpected value " + e.v }

// TestConcurrentQueriesWallTime complements the lock-based test with a
// realistic latency check: two 300ms nodes answer two queries in roughly
// one latency period, not two.
func TestConcurrentQueriesWallTime(t *testing.T) {
	r := NewRouter(time.Minute)
	n1 := NewNode("Node-1", 300*time.Millisecond)
	n2 := NewNode("Node-2", 300*time.Millisecond)
	r.AddNode(n1)
	r.AddNode(n2)
	n1.Put("Apple", "apple-value-from-Node-1")
	n2.Put("Zoo", "zoo-value-from-Node-2")
	if _, err := r.Grant(KeyRange{Start: "A"}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, _, err := r.Split("M", "Node-2"); err != nil {
		t.Fatalf("split: %v", err)
	}
	gw := NewGateway(r)

	start := time.Now()
	var wg sync.WaitGroup
	for _, key := range []string{"Apple", "Zoo"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			if _, err := gw.Query(k); err != nil {
				t.Errorf("query %q: %v", k, err)
			}
		}(key)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("concurrent queries took %v; serial execution would take >= 600ms", elapsed)
	}
}

func TestSplitChainKeepsFencingEpochs(t *testing.T) {
	r := NewRouter(time.Minute)
	for _, id := range []string{"Node-1", "Node-2", "Node-3"} {
		r.AddNode(NewNode(id, 0))
	}
	root, err := r.Grant(KeyRange{Start: "A"}, "Node-1")
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, right, err := r.Split("M", "Node-2"); err != nil {
		t.Fatalf("split at M: %v", err)
	} else if _, farRight, err := r.Split("S", "Node-3"); err != nil {
		t.Fatalf("split at S: %v", err)
	} else {
		if farRight.Range != (KeyRange{Start: "S", End: ""}) || farRight.NodeID != "Node-3" {
			t.Fatalf("far-right lease = %s on %s", farRight.Range, farRight.NodeID)
		}
		if farRight.Epoch != right.Epoch+1 {
			t.Fatalf("epoch = %d, want parent+1 = %d", farRight.Epoch, right.Epoch+1)
		}
		if farRight.Parent != right.ID {
			t.Fatalf("parent = #%d, want #%d", farRight.Parent, right.ID)
		}
	}
	if got := len(r.Leases()); got != 3 {
		t.Fatalf("lease table size = %d, want 3", got)
	}
	for key, want := range map[string]string{"Apple": "Node-1", "Mango": "Node-2", "Zoo": "Node-3"} {
		lease, _, err := r.Route(key)
		if err != nil || lease.NodeID != want {
			t.Fatalf("route %q -> %v, %v; want %s", key, lease, err, want)
		}
	}
	_ = root
}

func TestLeaseExpiry(t *testing.T) {
	r := NewRouter(30 * time.Millisecond)
	n1 := NewNode("Node-1", 0)
	r.AddNode(n1)
	n1.Put("Apple", "v")
	if _, err := r.Grant(KeyRange{Start: "A"}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	gw := NewGateway(r)
	time.Sleep(50 * time.Millisecond)
	if _, err := gw.Query("Apple"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("query with expired lease: err = %v, want expiry error", err)
	}
}

func TestSplitAndGrantErrors(t *testing.T) {
	r := NewRouter(time.Minute)
	r.AddNode(NewNode("Node-1", 0))
	r.AddNode(NewNode("Node-2", 0))
	if _, err := r.Grant(KeyRange{Start: "A"}, "Node-1"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if _, _, err := r.Split("0", "Node-2"); err == nil {
		t.Fatal("split outside any range must fail")
	}
	if _, _, err := r.Split("A", "Node-2"); err == nil {
		t.Fatal("split at range start must fail")
	}
	if _, _, err := r.Split("M", "Node-99"); err == nil {
		t.Fatal("split granting to unknown node must fail")
	}
	if _, err := r.Grant(KeyRange{Start: "G", End: "H"}, "Node-2"); err == nil {
		t.Fatal("overlapping grant must fail")
	}
	if _, err := r.Grant(KeyRange{Start: "G"}, "Node-99"); err == nil {
		t.Fatal("grant to unknown node must fail")
	}
	if _, _, err := r.Route("0"); err == nil {
		t.Fatal("routing a key below all ranges must fail")
	}
}
