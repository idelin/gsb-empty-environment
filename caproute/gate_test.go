package caproute

import (
	"context"
	"testing"
	"time"
)

// TestResourceGateLeaseBeforeClose pins the ordering contract: a lease won
// before close starts is honored to completion, while every later acquire
// fails and close returns only after the drain.
func TestResourceGateLeaseBeforeClose(t *testing.T) {
	gate := &resourceGate{rtype: TypeFile, label: "gated"}

	if !gate.acquire() {
		t.Fatal("first acquire on an open gate must succeed")
	}

	closed := make(chan struct{})
	go func() {
		gate.close()
		close(closed)
	}()

	// While the lease is held, close cannot return and no new lease is granted.
	select {
	case <-closed:
		t.Fatal("close returned while a lease was still held")
	default:
	}
	acquired := gate.acquire()
	if acquired {
		// Close had not started yet; release this extra lease and retry once
		// close is pending.
		gate.release()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-closed:
				t.Fatal("close returned while the original lease was held")
			default:
			}
			if !gate.acquire() {
				acquired = false
				break
			}
			gate.release()
			time.Sleep(time.Millisecond)
		}
	}
	if acquired {
		t.Fatal("acquire after close began must fail")
	}

	gate.release()
	<-closed
	if gate.acquire() {
		t.Fatal("acquire on a closed gate must fail")
	}

	// Closing again is a no-op that still waits on the (empty) WaitGroup.
	gate.close()
}

// TestServeRejectsWhenGateClosed reaches the resource_closed audit path that
// the host-level API normally short-circuits by revoking handles first.
func TestServeRejectsWhenGateClosed(t *testing.T) {
	audit := &MemoryAudit{}
	host := NewHost(WithAudit(audit))
	gate := &resourceGate{
		rtype: TypeFile,
		label: "gated",
		resource: ResourceFunc(func(context.Context, Operation, []byte, CallChain) ([]byte, error) {
			t.Fatal("resource callback must not run on a closed gate")
			return nil, nil
		}),
	}
	gate.closed = true
	capb := &capability{
		owner:   "alpha",
		gate:    gate,
		rtype:   TypeFile,
		allowed: opSetOf(OpFileRead),
	}
	chain := singleChain("alpha", OpFileRead)
	_, err := host.serve(context.Background(), "alpha", capb, OpFileRead, nil, chain)
	if got := denialReason(t, err); got != ReasonResourceClosed {
		t.Fatalf("closed gate: got reason %s", got)
	}
	found := false
	for _, ev := range audit.Events() {
		if ev.Reason == ReasonResourceClosed {
			found = true
		}
	}
	if !found {
		t.Fatal("audit trail missing resource_closed event")
	}
}

func TestChainIntersectionMonotonic(t *testing.T) {
	chain := CallChain{
		{Instance: "a", allowed: opSetOf(OpFileRead, OpFileWrite, OpFileStat)},
		{Instance: "b", allowed: opSetOf(OpFileRead, OpFileStat)},
		{Instance: "c", allowed: opSetOf(OpFileRead)},
	}
	eff := makeOpSet(chain.Effective())
	if len(eff) != 1 || !eff.contains(OpFileRead) {
		t.Fatalf("intersection should be {read}, got %v", chain.Effective())
	}

	var empty CallChain
	if len(empty.Effective()) != 0 {
		t.Fatal("empty chain must have no effective permissions")
	}

	// Intersection never allocates rights absent from either operand.
	left := opSetOf(OpNetSend)
	right := opSetOf(OpNetSend, OpNetReceive)
	if got := left.intersect(right); len(got) != 1 || !got.contains(OpNetSend) {
		t.Fatalf("narrow intersection changed rights: %v", got.toList())
	}
}
