package netsec

import (
	"errors"
	"testing"
)

// newTestHost builds a host with a high-integrity victim holding a read-only
// handle to obj1, a high-integrity peer, and a low-integrity attacker.
func newTestHost(t *testing.T) (*Host, ProcessRef, ProcessRef, ProcessRef, HandleRef) {
	t.Helper()
	h := NewHost(0x5eed)
	h.AddObject(obj1, FileType)
	h.AddObject(obj2, FileType)
	victim := h.AddProcess(procVictim, IntegrityHigh)
	peer := h.AddProcess(procPeer, IntegrityHigh)
	attacker := h.AddProcess(procAttacker, IntegrityLow)
	href, err := h.OpenHandle(victim, obj1, AccessRead, false)
	if err != nil {
		t.Fatalf("open victim handle: %v", err)
	}
	return h, victim, peer, attacker, href
}

func TestOpenExpandsGenericBits(t *testing.T) {
	h := NewHost(1)
	h.AddObject(obj1, FileType)
	p := h.AddProcess(1, IntegrityHigh)
	ref, err := h.OpenHandle(p, obj1, GenericRead, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, ok := h.EffectiveAccess(1, ref.Slot)
	if !ok || got != AccessRead {
		t.Fatalf("effective access = %v, ok=%v; want AccessRead", got, ok)
	}
}

func TestDuplicateRequiresSubsetOfGranted(t *testing.T) {
	h, victim, peer, _, href := newTestHost(t)

	for _, bad := range []Access{AccessWrite, GenericWrite, AccessRead | AccessWrite} {
		_, err := h.Duplicate(DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, DesiredAccess: bad})
		if !errors.Is(err, ErrAccessNotGranted) {
			t.Fatalf("desired=%x: err=%v; want ErrAccessNotGranted", bad, err)
		}
	}

	ref, err := h.Duplicate(DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, DesiredAccess: GenericRead})
	if err != nil {
		t.Fatalf("generic read dup should succeed: %v", err)
	}
	got, _ := h.EffectiveAccess(procPeer, ref.Slot)
	if got != AccessRead {
		t.Fatalf("peer effective access = %v; want AccessRead", got)
	}
}

func TestDuplicateSameAccessConflict(t *testing.T) {
	h, victim, peer, _, href := newTestHost(t)
	_, err := h.Duplicate(DuplicateRequest{
		Source: victim, SourceHandle: href, Target: peer,
		SameAccess: true, DesiredAccess: GenericWrite,
	})
	if !errors.Is(err, ErrAccessConflict) {
		t.Fatalf("err=%v; want ErrAccessConflict", err)
	}
}

func TestStaleHandleRejectedAfterSlotReuse(t *testing.T) {
	h, victim, peer, _, href := newTestHost(t)
	if err := h.CloseHandle(victim, href); err != nil {
		t.Fatalf("close: %v", err)
	}
	newRef, err := h.OpenHandle(victim, obj2, AccessRead, false)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if newRef.Slot != href.Slot {
		t.Fatalf("expected slot reuse, got %d then %d", href.Slot, newRef.Slot)
	}
	_, err = h.Duplicate(DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, SameAccess: true})
	if !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("stale dup: err=%v; want ErrStaleHandle", err)
	}
	if _, err := h.Duplicate(DuplicateRequest{Source: victim, SourceHandle: newRef, Target: peer, SameAccess: true}); err != nil {
		t.Fatalf("fresh dup should succeed: %v", err)
	}
}

func TestObjectGenerationChecked(t *testing.T) {
	h, victim, peer, _, href := newTestHost(t)
	h.RetireObject(obj1)
	_, err := h.Duplicate(DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, SameAccess: true})
	if !errors.Is(err, ErrStaleObject) {
		t.Fatalf("err=%v; want ErrStaleObject", err)
	}
}

func TestCrossIntegrityBroker(t *testing.T) {
	h, victim, _, attacker, href := newTestHost(t)
	req := func(cap *Capability) DuplicateRequest {
		return DuplicateRequest{Source: victim, SourceHandle: href, Target: attacker, SameAccess: true, Broker: cap}
	}

	if _, err := h.Duplicate(req(nil)); !errors.Is(err, ErrBrokerRequired) {
		t.Fatalf("no broker: err=%v; want ErrBrokerRequired", err)
	}

	forged := &Capability{Source: victim, Target: attacker, ObjectType: FileType.Name}
	if _, err := h.Duplicate(req(forged)); !errors.Is(err, ErrBrokerRequired) {
		t.Fatalf("forged broker: err=%v; want ErrBrokerRequired", err)
	}

	cap, err := h.IssueBrokerCapability(victim, attacker, FileType.Name)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := h.Duplicate(req(cap)); err != nil {
		t.Fatalf("valid broker should pass: %v", err)
	}

	h.RestartProcess(procAttacker)
	if _, err := h.Duplicate(req(cap)); !errors.Is(err, ErrNoSuchProcess) {
		t.Fatalf("stale target after restart: err=%v; want ErrNoSuchProcess", err)
	}
}

func TestBatchAtomic(t *testing.T) {
	h, victim, peer, _, href := newTestHost(t)
	good := DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, SameAccess: true}
	bad := DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, DesiredAccess: AccessWrite}

	if _, err := h.DuplicateBatch([]DuplicateRequest{good, bad}); err == nil {
		t.Fatal("batch with invalid item should fail")
	}
	if n := h.HandleCount(procPeer); n != 0 {
		t.Fatalf("failed batch left %d partial handles; want 0", n)
	}

	refs, err := h.DuplicateBatch([]DuplicateRequest{good, good})
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if len(refs) != 2 || h.HandleCount(procPeer) != 2 {
		t.Fatalf("valid batch published %d handles; want 2", h.HandleCount(procPeer))
	}
}

// TestReviewBatchLeaksPartial documents flaw 6 of the pending-review host.
func TestReviewBatchLeaksPartial(t *testing.T) {
	r := NewReviewHost(0x5eed)
	r.AddObject(obj1, FileType)
	victim := r.AddProcess(procVictim, IntegrityHigh)
	peer := r.AddProcess(procPeer, IntegrityHigh)
	href, err := r.OpenHandle(victim, obj1, AccessRead, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	good := DuplicateRequest{Source: victim, SourceHandle: href, Target: peer, SameAccess: true}
	bad := DuplicateRequest{Source: victim, SourceHandle: HandleRef{Slot: 99}, Target: peer, SameAccess: true}
	refs, err := r.DuplicateBatch([]DuplicateRequest{good, bad})
	if err == nil {
		t.Fatal("expected batch failure")
	}
	if len(refs) != 1 || r.HandleCount(procPeer) != 1 {
		t.Fatalf("review host should leak 1 partial handle, got refs=%v count=%d", refs, r.HandleCount(procPeer))
	}
}
