package caproute

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReloadStaleGeneration(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	old := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	if gen := host.Generation("alpha"); gen != 1 {
		t.Fatalf("initial generation = %d, want 1", gen)
	}

	gen, err := host.Reload("alpha")
	if err != nil || gen != 2 {
		t.Fatalf("reload returned gen=%d err=%v", gen, err)
	}

	// Handles minted before the reload are stale even though the resource
	// itself is still open and serving other instances.
	_, err = host.Entry(context.Background(), "alpha", old, OpFileRead, nil)
	if got := denialReason(t, err); got != ReasonStaleGeneration {
		t.Fatalf("stale handle: got reason %s", got)
	}

	// Fresh capabilities work and a further reload invalidates them too:
	// generations are non-rewindable.
	fresh := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	if _, err := host.Entry(context.Background(), "alpha", fresh, OpFileRead, nil); err != nil {
		t.Fatalf("fresh handle should work: %v", err)
	}
	if _, err := host.Reload("alpha"); err != nil {
		t.Fatal(err)
	}
	_, err = host.Entry(context.Background(), "alpha", fresh, OpFileRead, nil)
	if got := denialReason(t, err); got != ReasonStaleGeneration {
		t.Fatalf("handle after second reload: got reason %s", got)
	}

	// Reload of an unknown instance is a denial.
	_, err = host.Reload("ghost")
	if got := denialReason(t, err); got != ReasonUnknownInstance {
		t.Fatalf("reload ghost: got reason %s", got)
	}
}

func TestUnloadAndReregisterKeepsHandlesInvalid(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	h := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})

	if err := host.UnloadInstance("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Entry(context.Background(), "alpha", h, OpFileRead, nil); err == nil {
		t.Fatal("unloaded instance must not invoke resources")
	} else if got := denialReason(t, err); got != ReasonUnknownInstance {
		t.Fatalf("unloaded caller: got reason %s", got)
	}

	// Re-registering starts a fresh lineage; the old handle stays invalid.
	if err := host.RegisterInstance("alpha"); err != nil {
		t.Fatal(err)
	}
	_, err := host.Entry(context.Background(), "alpha", h, OpFileRead, nil)
	if got := denialReason(t, err); got != ReasonRevokedHandle {
		t.Fatalf("old handle after re-register: got reason %s", got)
	}
}

func TestUnloadPreservesSharedAndUnallocatedResources(t *testing.T) {
	host, _ := setupHost(t)
	shared := newTrackResource(false)
	idle := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, shared, "shared"); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterResource(TypeFile, idle, "idle"); err != nil {
		t.Fatal(err)
	}
	alphaHandle := mint(t, host, "alpha", TypeFile, "shared", []Operation{OpFileRead})
	betaHandle := mint(t, host, "beta", TypeFile, "shared", []Operation{OpFileRead})

	if err := host.UnloadInstance("alpha"); err != nil {
		t.Fatal(err)
	}

	// alpha's handle is dead, but beta keeps working through the same gate.
	if _, err := host.Entry(context.Background(), "beta", betaHandle, OpFileRead, nil); err != nil {
		t.Fatalf("shared resource must survive unload: %v", err)
	}
	_, err := host.Entry(context.Background(), "alpha", alphaHandle, OpFileRead, nil)
	if denialReason(t, err) != ReasonUnknownInstance {
		t.Fatalf("unloaded caller: got %v", err)
	}

	// A resource never handed to the unloaded instance stays registered and
	// can still receive freshly minted capabilities.
	gammaHandle := mint(t, host, "beta", TypeFile, "idle", []Operation{OpFileRead})
	if _, err := host.Entry(context.Background(), "beta", gammaHandle, OpFileRead, nil); err != nil {
		t.Fatalf("unallocated resource must not be closed: %v", err)
	}
	if calls, _ := idle.snapshot(); calls != 1 {
		t.Fatalf("idle resource calls = %d, want 1", calls)
	}
}

func TestConcurrentCloseDrainAndReject(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(true)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	h := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})

	// Start an operation that holds its lease inside the resource callback.
	callDone := make(chan error, 1)
	go func() {
		_, err := host.Entry(context.Background(), "alpha", h, OpFileRead, nil)
		callDone <- err
	}()
	select {
	case <-res.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight operation never entered the resource")
	}

	// Close must block while the lease is held.
	closeDone := make(chan struct{})
	go func() {
		if err := host.CloseResource(TypeFile, "doc"); err != nil {
			t.Errorf("close: %v", err)
		}
		close(closeDone)
	}()
	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight operation finished")
	case <-time.After(50 * time.Millisecond):
	}

	// Once close has started, new entries are refused immediately.
	_, err := host.Entry(context.Background(), "alpha", h, OpFileRead, nil)
	if got := denialReason(t, err); got != ReasonRevokedHandle {
		// Handles are marked revoked before the gate wait, so the caller
		// sees a revocation; either way entry is impossible.
		t.Fatalf("entry during close: got reason %s", got)
	}

	// Release the in-flight call: it is allowed to finish successfully.
	close(res.release)
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("leased operation should finish, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight operation did not finish")
	}
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after drain")
	}

	// After close returns, every later attempt is refused and no callback runs.
	callsBefore, _ := res.snapshot()
	if _, err := host.Entry(context.Background(), "alpha", h, OpFileRead, nil); err == nil {
		t.Fatal("closed resource must reject all later calls")
	}
	callsAfter, _ := res.snapshot()
	if callsAfter != callsBefore {
		t.Fatalf("a callback ran after close: before=%d after=%d", callsBefore, callsAfter)
	}
}

func TestCloseAllowsManyConcurrentLeases(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(true)
	if err := host.RegisterResource(TypeNetwork, res, "sock"); err != nil {
		t.Fatal(err)
	}
	h := mint(t, host, "alpha", TypeNetwork, "sock", []Operation{OpNetSend})

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = host.Entry(context.Background(), "alpha", h, OpNetSend, nil)
		}()
	}
	for i := 0; i < workers; i++ {
		select {
		case <-res.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not enter the resource")
		}
	}

	closed := make(chan struct{})
	go func() { _ = host.CloseResource(TypeNetwork, "sock"); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close returned while leases were still held")
	case <-time.After(50 * time.Millisecond):
	}

	for i := 0; i < workers; i++ {
		res.release <- struct{}{}
	}
	wg.Wait()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not drain all leases")
	}
	calls, _ := res.snapshot()
	if calls != workers {
		t.Fatalf("expected %d completed calls, got %d", workers, calls)
	}
}

func TestAuditReasonsAndTenantIsolation(t *testing.T) {
	host, audit := setupHost(t)
	res := newTrackResource(false)
	const secretLabel = "tenant-beta-secret-ledger"
	if err := host.RegisterResource(TypeFile, res, secretLabel); err != nil {
		t.Fatal(err)
	}
	betaHandle := mint(t, host, "beta", TypeFile, secretLabel, []Operation{OpFileRead})

	// alpha tries to wield beta's handle and to guess random bytes.
	_, foreignErr := host.Entry(context.Background(), "alpha", betaHandle, OpFileRead, nil)
	_, guessedErr := host.Entry(context.Background(), "alpha", []byte("0123456789abcdef0123456789abcdef"), OpFileRead, nil)

	for _, err := range []error{foreignErr, guessedErr} {
		if strings.Contains(err.Error(), secretLabel) || strings.Contains(err.Error(), "beta") || strings.Contains(err.Error(), "ledger") {
			t.Fatalf("caller-visible error leaks another tenant's identity: %q", err.Error())
		}
	}

	// The host-side audit trail still records structured reasons and, for
	// resolved handles, the true resource label.
	var sawForeign, sawInvalid bool
	for _, ev := range audit.Events() {
		if ev.Reason == ReasonInvalidHandle {
			if ev.Label == secretLabel {
				sawForeign = true
			} else {
				sawInvalid = true
			}
		}
	}
	if !sawForeign || !sawInvalid {
		t.Fatalf("audit missing denial records: foreign=%v forged=%v events=%+v", sawForeign, sawInvalid, audit.Events())
	}

	// Every distinct policy path exercised above is represented by a reason.
	want := map[Reason]bool{
		ReasonTypeMismatch:    false,
		ReasonWidening:        false,
		ReasonStaleGeneration: false,
		ReasonChainCycle:      false,
		ReasonChainDepth:      false,
		ReasonInvalidHandle:   true,
	}

	doc := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, doc, "doc"); err != nil {
		t.Fatal(err)
	}
	alphaHandle := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	_, _ = host.Entry(context.Background(), "alpha", alphaHandle, OpNetDial, nil) // type mismatch
	_, _ = host.Delegate("alpha", alphaHandle, []Operation{OpFileWrite})          // widening
	_, _ = host.Reload("alpha")
	_, _ = host.Entry(context.Background(), "alpha", alphaHandle, OpFileRead, nil) // stale
	cycleHandle := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	_, _ = host.Proxy(context.Background(), "alpha", cycleHandle, OpFileRead, nil,
		CallChain{
			{Instance: "beta", Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)},
			{Instance: "alpha", Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)},
		}) // cycle: alpha already appears in the chain
	deep := make(CallChain, MaxChainDepth)
	for i := range deep {
		deep[i] = ChainFrame{Instance: InstanceID("deep-" + itoa(i)), Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)}
	}
	_, _ = host.Proxy(context.Background(), "alpha", cycleHandle, OpFileRead, nil, deep) // depth exceeded

	for _, ev := range audit.Events() {
		if _, ok := want[ev.Reason]; ok {
			want[ev.Reason] = true
		}
		if ev.Reason != "" && strings.Contains(ev.Detail, secretLabel) {
			t.Fatal("audit detail must use Label, not embed it in free text")
		}
	}

	for reason, seen := range want {
		if !seen {
			t.Errorf("audit never recorded reason %s", reason)
		}
	}
}
