package pending_test

import (
	"testing"

	"example.org/netsec/q0070"
	"example.org/netsec/q0070/pending"
)

func TestPendingFinalizerRestoresRevokedResource(t *testing.T) {
	rt := pending.NewRuntime()
	resource := rt.NewResource("secret")

	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, ok := rt.IssueTicket(strong)
	if !ok {
		t.Fatal("could not issue pre-revoke ticket")
	}

	// Preload g1 to exercise slot overwrite. The vulnerable fixture does not
	// refcount globals, which is part of the pending audit's bad behavior.
	oldResource := rt.NewResource("old-global")
	oldStrong, _, err := rt.Allocate(oldResource)
	if err != nil {
		t.Fatal(err)
	}
	rt.PutGlobalSlot("g1", oldStrong)

	// The fixture records host policy but intentionally ignores it later.
	rt.SetHostResurrectionAllowed(strong, false)
	rt.SetFinalizer(func(ctx *pending.FinalizeContext) {
		if _, ok := ctx.WeakGet(); ok {
			t.Error("callback WeakGet succeeded")
		}
		ctx.RequestResurrect("g1")
		ctx.RequestResurrect("g2")
	})

	rt.Drop(strong)
	rt.Revoke(strong.ObjectRef)

	ran, resurrected := rt.FinalizeNext()
	if !ran || !resurrected {
		t.Fatalf("vulnerable finalizer did not resurrect: ran=%t resurrected=%t", ran, resurrected)
	}

	// This is the actual violation: a real host call, not merely a finalizer
	// count.
	if !rt.Call(ticket) {
		t.Fatal("pending fixture did not reproduce post-revoke resource call")
	}
	live, calls, clearings, ok := rt.ResourceInfo(resource)
	if !ok || !live || calls != 1 || clearings != 1 {
		t.Fatalf("resource state live=%t calls=%d clearings=%d ok=%t", live, calls, clearings, ok)
	}

	object, ok := rt.InspectRef(strong.ObjectRef)
	if !ok {
		t.Fatal("no object snapshot")
	}
	if object.State != netsec.StateLive || !object.Revoked || object.CapGen != 1 {
		t.Fatalf("vulnerable object state: %+v", object)
	}
	if object.HostAllowed {
		t.Fatal("host policy should have remained false")
	}
	if object.WeakGetAttempts != 1 || object.WeakGetSuccesses != 0 {
		t.Fatalf("unexpected callback WeakGet counters: %+v", object)
	}
	if len(object.ResurrectionRequests) != 2 {
		t.Fatalf("expected two requests, got %+v", object.ResurrectionRequests)
	}
	for _, request := range object.ResurrectionRequests {
		if !request.Accepted || request.Reason != "VULNERABLE_ACCEPTED" {
			t.Fatalf("pending fixture should wrongly accept: %+v", request)
		}
	}
	if ref, _ := rt.GlobalSlot("g1"); ref != strong.ObjectRef {
		t.Fatalf("g1 overwrite failed: %+v", ref)
	}
	if ref, _ := rt.GlobalSlot("g2"); ref != strong.ObjectRef {
		t.Fatalf("g2 resurrection failed: %+v", ref)
	}
}

func TestPendingAddressReuseStillRejectsStaleTicket(t *testing.T) {
	rt := pending.NewRuntime()
	resourceA := rt.NewResource("A")
	strongA, weakA, err := rt.Allocate(resourceA)
	if err != nil {
		t.Fatal(err)
	}
	ticketA, _ := rt.IssueTicket(strongA)
	rt.SetFinalizer(func(*pending.FinalizeContext) {
		// No resurrection request: this path correctly dies.
	})
	rt.Drop(strongA)
	if ran, resurrected := rt.FinalizeNext(); !ran || resurrected {
		t.Fatalf("unexpected finalization: ran=%t resurrected=%t", ran, resurrected)
	}

	resourceB := rt.NewResource("B")
	strongB, weakB, err := rt.Allocate(resourceB)
	if err != nil {
		t.Fatal(err)
	}
	if strongB.Address != strongA.Address {
		t.Fatalf("address not reused: A=%d B=%d", strongA.Address, strongB.Address)
	}
	if strongB.ID == strongA.ID || strongB.AllocGen <= strongA.AllocGen {
		t.Fatalf("reuse did not advance generations: A=%+v B=%+v", strongA.ObjectRef, strongB.ObjectRef)
	}
	if _, ok := rt.WeakGet(weakA); ok {
		t.Fatal("old weak reference upgraded")
	}
	if rt.Call(ticketA) {
		t.Fatal("old ticket reached new resource")
	}
	if _, ok := rt.WeakGet(weakB); !ok {
		t.Fatal("new weak reference failed")
	}
	live, calls, _, ok := rt.ResourceInfo(resourceA)
	if !ok || live || calls != 0 {
		t.Fatalf("old resource live=%t calls=%d ok=%t", live, calls, ok)
	}
}
