package netsec_test

import (
	"sync"
	"testing"

	netsec "example.org/netsec/q0070"
)

func TestRevokedCapabilityCannotBeResurrected(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, weak, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, ok := rt.IssueTicket(strong)
	if !ok {
		t.Fatal("could not issue pre-revoke ticket")
	}
	if !rt.SetHostResurrectionAllowed(strong, true) {
		t.Fatal("host could not set resurrection policy")
	}

	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		if _, ok := ctx.WeakGet(); ok {
			t.Error("WeakGet succeeded from a finalizing callback")
		}
		if !ctx.RequestResurrect("g1") || !ctx.RequestResurrect("g2") {
			t.Error("finalizer requests were not recorded")
		}
	})

	if result := rt.StrongDrop(strong); !result.FinalizeQueued {
		t.Fatalf("finalizer not queued: %+v", result)
	}
	revoked := rt.Revoke(strong.ObjectRef)
	if !revoked.Changed || revoked.CapGen != 1 || !revoked.ResourceCleared {
		t.Fatalf("unexpected revoke result: %+v", revoked)
	}
	if _, ok := rt.WeakGet(weak); ok {
		t.Fatal("WeakGet succeeded after the strong count reached zero")
	}

	report, ok := rt.FinalizeNext()
	if !ok {
		t.Fatal("queued finalizer did not run")
	}
	if report.FinalState != netsec.StateDead {
		t.Fatalf("revoked object resurrected: %+v", report)
	}
	if report.WeakGetAttempts != 1 || report.WeakGetSuccesses != 0 {
		t.Fatalf("unexpected callback WeakGet counters: %+v", report)
	}
	if len(report.Requests) != 2 {
		t.Fatalf("both requests must be recorded: %+v", report.Requests)
	}
	for _, request := range report.Requests {
		if request.Accepted || request.Reason != "capability_revoked" {
			t.Fatalf("request should be denied: %+v", request)
		}
	}
	if _, ok := rt.GlobalSlot("g1"); ok {
		t.Fatal("revoked object was installed in global slot")
	}

	call := rt.Call(ticket)
	if call.Success || call.Calls != 0 {
		t.Fatalf("stale ticket reached resource: %+v", call)
	}
	info, _ := rt.ResourceInfo(resource)
	if info.Live || info.Calls != 0 || info.Clearings != 1 {
		t.Fatalf("resource was not cleared exactly once or was called: %+v", info)
	}
	object, _ := rt.Inspect(strong.ObjectRef)
	if object.State != netsec.StateDead || object.ResourceID != 0 ||
		object.LastResourceID != resource || object.CapGen != 1 {
		t.Fatalf("unexpected dead object state: %+v", object)
	}
}

func TestHostApprovedResurrectionStillRejectsSecondRequest(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, ok := rt.IssueTicket(strong)
	if !ok {
		t.Fatal("could not issue ticket")
	}
	if !rt.SetHostResurrectionAllowed(strong, true) {
		t.Fatal("could not allow resurrection")
	}

	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		if _, ok := ctx.WeakGet(); ok {
			t.Error("callback WeakGet must be rejected while finalizing")
		}
		ctx.RequestResurrect("primary")
		ctx.RequestResurrect("secondary")
	})

	if result := rt.StrongDrop(strong); !result.FinalizeQueued {
		t.Fatalf("finalizer not queued: %+v", result)
	}
	report, ok := rt.FinalizeNext()
	if !ok {
		t.Fatal("finalizer did not run")
	}
	if report.FinalState != netsec.StateLive || report.Slot != "primary" {
		t.Fatalf("host-approved resurrection failed: %+v", report)
	}
	if len(report.Requests) != 2 ||
		!report.Requests[0].Accepted ||
		report.Requests[1].Accepted ||
		report.Requests[1].Reason != "duplicate_request" {
		t.Fatalf("unexpected request decisions: %+v", report.Requests)
	}
	if _, ok := rt.GlobalSlot("secondary"); ok {
		t.Fatal("second request must not create an alias")
	}
	info, _ := rt.GlobalSlot("primary")
	if info.Object != strong.ObjectRef || info.State != netsec.StateLive {
		t.Fatalf("wrong primary slot owner: %+v", info)
	}
	if report.WeakGetAttempts != 1 || report.WeakGetSuccesses != 0 {
		t.Fatalf("callback WeakGet was not denied: %+v", report)
	}

	call := rt.Call(ticket)
	if !call.Success || call.Calls != 1 {
		t.Fatalf("host-approved live resurrection should preserve the valid ticket: %+v", call)
	}
}

func TestRevokeDuringFinalizeCannotRestoreResource(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ := rt.IssueTicket(strong)
	rt.SetHostResurrectionAllowed(strong, true)

	entered := make(chan struct{})
	release := make(chan struct{})
	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		close(entered)
		<-release
		ctx.RequestResurrect("g")
	})
	rt.StrongDrop(strong)

	done := make(chan netsec.FinalizeReport, 1)
	go func() {
		report, _ := rt.FinalizeNext()
		done <- report
	}()

	<-entered
	revoked := rt.Revoke(strong.ObjectRef)
	close(release)
	report := <-done

	if !revoked.Changed || report.FinalState != netsec.StateDead {
		t.Fatalf("revoke did not win finalization decision: revoke=%+v report=%+v", revoked, report)
	}
	if len(report.Requests) != 1 || report.Requests[0].Accepted ||
		report.Requests[0].Reason != "capability_revoked" {
		t.Fatalf("unexpected finalization requests: %+v", report.Requests)
	}
	call := rt.Call(ticket)
	if call.Success || call.Calls != 0 {
		t.Fatalf("resource call succeeded after concurrent revocation: %+v", call)
	}
	res, _ := rt.ResourceInfo(resource)
	if res.Live || res.Calls != 0 {
		t.Fatalf("resource state is unsafe: %+v", res)
	}
}

func TestRevokeAfterFinalizeInvalidatesRestoredTicket(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ := rt.IssueTicket(strong)
	rt.SetHostResurrectionAllowed(strong, true)
	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		ctx.RequestResurrect("g")
	})
	rt.StrongDrop(strong)

	report, ok := rt.FinalizeNext()
	if !ok || report.FinalState != netsec.StateLive {
		t.Fatalf("expected host-approved resurrection: %+v", report)
	}
	revoked := rt.Revoke(strong.ObjectRef)
	if !revoked.Changed || revoked.CapGen != 1 {
		t.Fatalf("unexpected revoke: %+v", revoked)
	}

	call := rt.Call(ticket)
	if call.Success || call.Reason != netsec.CallReasonCapabilityStale || call.Calls != 0 {
		t.Fatalf("old ticket should fail after post-finalization revoke: %+v", call)
	}
	res, _ := rt.ResourceInfo(resource)
	if res.Live || res.Calls != 0 {
		t.Fatalf("resource remained callable after revoke: %+v", res)
	}
}

func TestRepeatedDropDoesNotQueueSecondFinalizer(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetFinalizer(func(*netsec.FinalizeContext) {})

	if result := rt.StrongDrop(strong); !result.FinalizeQueued {
		t.Fatalf("first drop did not queue: %+v", result)
	}
	if got := rt.PendingFinalizers(); got != 1 {
		t.Fatalf("pending finalizers = %d, want 1", got)
	}
	rt.RunFinalizers(1)
	if got := rt.PendingFinalizers(); got != 0 {
		t.Fatalf("pending finalizers = %d, want 0", got)
	}

	again := rt.StrongDrop(strong)
	if again.FinalizeQueued {
		t.Fatalf("stale drop queued a second finalizer: %+v", again)
	}
	if got := rt.PendingFinalizers(); got != 0 {
		t.Fatalf("pending finalizers after stale drop = %d", got)
	}
}

func TestDropOfResurrectedSlotDoesNotRequeue(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	rt.SetHostResurrectionAllowed(strong, true)
	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		ctx.RequestResurrect("g")
	})
	rt.StrongDrop(strong)
	if report, _ := rt.FinalizeNext(); report.FinalState != netsec.StateLive {
		t.Fatalf("expected resurrection: %+v", report)
	}

	drop := rt.ClearGlobalSlot("g")
	if drop.FinalizeQueued {
		t.Fatalf("post-finalization slot drop requeued finalizer: %+v", drop)
	}
	if got := rt.PendingFinalizers(); got != 0 {
		t.Fatalf("pending finalizers = %d, want 0", got)
	}
	info, _ := rt.Inspect(strong.ObjectRef)
	if info.State != netsec.StateDead || info.ResourceID != 0 {
		t.Fatalf("drop of last resurrected owner should die directly: %+v", info)
	}
	res, _ := rt.ResourceInfo(resource)
	if res.Live || res.Calls != 0 {
		t.Fatalf("resource should be cleared without a second finalizer: %+v", res)
	}
}

func TestGlobalSlotOverwriteDropsPreviousOwner(t *testing.T) {
	rt := netsec.NewRuntime()
	resourceA := rt.NewResource("A")
	resourceB := rt.NewResource("B")
	a, _, err := rt.Allocate(resourceA)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := rt.Allocate(resourceB)
	if err != nil {
		t.Fatal(err)
	}
	aTicket, _ := rt.IssueTicket(a)
	if _, ok := rt.SetGlobalSlot("g", b); !ok {
		t.Fatal("could not install B")
	}
	if result := rt.StrongDrop(b); result.State != netsec.StateLive || result.FinalizeQueued {
		t.Fatalf("B still has its slot owner: %+v", result)
	}

	rt.SetHostResurrectionAllowed(a, true)
	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		ctx.RequestResurrect("g")
		ctx.RequestResurrect("g2")
	})
	rt.StrongDrop(a)

	report, ok := rt.FinalizeNext()
	if !ok || report.FinalState != netsec.StateLive || report.Slot != "g" {
		t.Fatalf("A did not replace slot g: %+v", report)
	}
	if report.PreviousSlotOwner == nil || report.PreviousSlotOwner.Object != b.ObjectRef {
		t.Fatalf("missing B slot-owner report: %+v", report)
	}
	if !report.SlotFinalizeQueued {
		t.Fatal("overwriting B should queue B's finalizer")
	}
	bInfo, _ := rt.Inspect(b.ObjectRef)
	if bInfo.State != netsec.StateFinalizing || rt.PendingFinalizers() != 1 {
		t.Fatalf("B was not finalized by slot replacement: %+v pending=%d", bInfo, rt.PendingFinalizers())
	}
	slot, _ := rt.GlobalSlot("g")
	if slot.Object != a.ObjectRef {
		t.Fatalf("slot g owner = %+v, want A", slot)
	}

	if call := rt.Call(aTicket); !call.Success || call.Calls != 1 {
		t.Fatalf("A ticket should remain valid under allowed resurrection: %+v", call)
	}
	rt.RunFinalizers(0)
	bRes, _ := rt.ResourceInfo(resourceB)
	if bRes.Live || bRes.Calls != 0 {
		t.Fatalf("B resource not cleared: %+v", bRes)
	}
}

func TestAddressReuseInvalidatesOldWeakAndTicket(t *testing.T) {
	rt := netsec.NewRuntime()
	resourceA := rt.NewResource("A")
	strongA, weakA, err := rt.Allocate(resourceA)
	if err != nil {
		t.Fatal(err)
	}
	ticketA, _ := rt.IssueTicket(strongA)
	rt.SetFinalizer(func(*netsec.FinalizeContext) {})
	rt.StrongDrop(strongA)
	report, ok := rt.FinalizeNext()
	if !ok || report.FinalState != netsec.StateDead {
		t.Fatalf("A did not die: %+v", report)
	}

	resourceB := rt.NewResource("B")
	strongB, weakB, err := rt.Allocate(resourceB)
	if err != nil {
		t.Fatal(err)
	}
	if strongB.Address != strongA.Address {
		t.Fatalf("address was not reused: A=%d B=%d", strongA.Address, strongB.Address)
	}
	if strongB.ID == strongA.ID || strongB.AllocGen <= strongA.AllocGen {
		t.Fatalf("reuse did not advance identity generations: A=%+v B=%+v", strongA.ObjectRef, strongB.ObjectRef)
	}
	if _, ok := rt.WeakGet(weakA); ok {
		t.Fatal("old weak reference upgraded after address reuse")
	}
	call := rt.Call(ticketA)
	if call.Success || call.Calls != 0 {
		t.Fatalf("old ticket reached the new resource: %+v", call)
	}
	if revoked := rt.Revoke(strongA.ObjectRef); revoked.Changed {
		t.Fatalf("revoking the old generation affected the reused address: %+v", revoked)
	}
	if dropped := rt.StrongDrop(strongA); dropped.Changed {
		t.Fatalf("stale strong token changed the reused address: %+v", dropped)
	}
	if _, ok := rt.WeakGet(weakB); !ok {
		t.Fatal("new weak reference should upgrade")
	}
	newTicket, ok := rt.IssueTicket(strongB)
	if !ok || !rt.Call(newTicket).Success {
		t.Fatal("new object should be callable")
	}
	resA, _ := rt.ResourceInfo(resourceA)
	if resA.Calls != 0 {
		t.Fatalf("old resource was invoked %d times", resA.Calls)
	}
}

func TestStrongCountAndWeakUpgradeBoundary(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	s1, weak, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	s2, ok := rt.StrongAdd(weak)
	if !ok {
		t.Fatal("StrongAdd failed")
	}
	s3, ok := rt.WeakGet(weak)
	if !ok {
		t.Fatal("WeakGet failed with strong owners")
	}
	s4, ok := rt.WeakGet(weak)
	if !ok {
		t.Fatal("second WeakGet failed")
	}

	if result := rt.StrongDrop(s1); result.StrongRefs != 3 || result.FinalizeQueued {
		t.Fatalf("unexpected drop: %+v", result)
	}
	if result := rt.StrongDrop(s2); result.StrongRefs != 2 || result.FinalizeQueued {
		t.Fatalf("unexpected drop: %+v", result)
	}
	if result := rt.StrongDrop(s3); result.StrongRefs != 1 || result.State != netsec.StateLive {
		t.Fatalf("unexpected drop: %+v", result)
	}
	// s4 is the only strong owner now.
	if result := rt.StrongDrop(s4); !result.FinalizeQueued || result.State != netsec.StateFinalizing {
		t.Fatalf("last drop did not queue finalization: %+v", result)
	}
	if _, ok := rt.WeakGet(weak); ok {
		t.Fatal("WeakGet succeeded after strong count reached zero")
	}
	if _, ok := rt.StrongAdd(weak); ok {
		t.Fatal("StrongAdd succeeded in finalizing state")
	}
}

func TestRevokeInvalidatesLiveTicketBeforeFinalization(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, weak, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ := rt.IssueTicket(strong)
	rt.Revoke(strong.ObjectRef)

	call := rt.Call(ticket)
	if call.Success || call.Reason != netsec.CallReasonCapabilityStale || call.Calls != 0 {
		t.Fatalf("stale capability ticket was not rejected: %+v", call)
	}
	if _, ok := rt.IssueTicket(strong); ok {
		t.Fatal("revoked object issued a new method ticket")
	}
	// WeakGet is intentionally still allowed while the live object has strong
	// owners; it grants no capability and cannot make an old ticket valid.
	if _, ok := rt.WeakGet(weak); !ok {
		t.Fatal("WeakGet should still enforce only live + strong > 0")
	}
}

func TestFinalizerWeakGetAttemptsAreSerialized(t *testing.T) {
	rt := netsec.NewRuntime()
	resource := rt.NewResource("secret")
	strong, _, err := rt.Allocate(resource)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	rt.SetFinalizer(func(ctx *netsec.FinalizeContext) {
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, ok := ctx.WeakGet(); ok {
					t.Error("WeakGet succeeded during finalization")
				}
			}()
		}
		wg.Wait()
	})
	rt.StrongDrop(strong)
	report, ok := rt.FinalizeNext()
	if !ok {
		t.Fatal("finalizer did not run")
	}
	if report.WeakGetAttempts != 8 || report.WeakGetSuccesses != 0 {
		t.Fatalf("unexpected attempts: %+v", report)
	}
}
