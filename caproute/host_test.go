package caproute

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// trackResource records every callback and can block inside Serve until a
// test releases it, modelling an in-flight operation holding a lease.
type trackResource struct {
	mu      sync.Mutex
	calls   int
	active  int
	maxSeen int
	entered chan struct{}
	release chan struct{}
	block   bool
}

func newTrackResource(block bool) *trackResource {
	return &trackResource{block: block, entered: make(chan struct{}, 32), release: make(chan struct{})}
}

func (t *trackResource) Serve(ctx context.Context, op Operation, payload []byte, chain CallChain) ([]byte, error) {
	t.mu.Lock()
	t.calls++
	t.active++
	if t.active > t.maxSeen {
		t.maxSeen = t.active
	}
	t.mu.Unlock()

	if t.block {
		t.entered <- struct{}{}
		<-t.release
	}

	t.mu.Lock()
	t.active--
	t.mu.Unlock()
	return []byte("ok:" + string(op)), nil
}

func (t *trackResource) snapshot() (calls, maxSeen int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls, t.maxSeen
}

func denialReason(t *testing.T, err error) Reason {
	t.Helper()
	var d *DenialError
	if !errors.As(err, &d) {
		t.Fatalf("expected *DenialError, got %T: %v", err, err)
	}
	return d.Reason
}

func mustHandle(t *testing.T, h Handle, err error) Handle {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected mint error: %v", err)
	}
	if len(h) == 0 {
		t.Fatal("minted empty handle")
	}
	return h
}

// mint wraps the two-value mint call so call sites stay single-line.
func mint(t *testing.T, host *Host, owner InstanceID, rtype ResourceType, label string, ops []Operation) Handle {
	t.Helper()
	h, err := host.MintCapability(owner, rtype, label, ops)
	return mustHandle(t, h, err)
}

func setupHost(t *testing.T) (*Host, *MemoryAudit) {
	t.Helper()
	audit := &MemoryAudit{}
	host := NewHost(WithAudit(audit))
	if err := host.RegisterInstance("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterInstance("beta"); err != nil {
		t.Fatal(err)
	}
	return host, audit
}

func opSetOf(ops ...Operation) opSet { return makeOpSet(ops) }

func singleChain(id InstanceID, ops ...Operation) CallChain {
	return CallChain{{Instance: id, Allowed: ops, allowed: opSetOf(ops...)}}
}
