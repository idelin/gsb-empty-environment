package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type recordingCapability struct {
	mu    sync.Mutex
	calls []MethodID
}

func (c *recordingCapability) Call(_ context.Context, method MethodID, _ []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method)
	return []byte(fmt.Sprintf("method-%d", method)), nil
}

func (c *recordingCapability) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func TestGuessedExportIDIsFatalAndDisconnects(t *testing.T) {
	ctx := context.Background()
	victim := NewExportTable("conn-victim")
	victimCap := &recordingCapability{}
	victimID, err := victim.Export(victimCap, 7)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	attacker := NewExportTable("conn-attacker")
	if _, err := attacker.Dispatch(ctx, Call{Target: victimID, Method: 7}); !errors.Is(err, ErrUnknownExport) {
		t.Fatalf("guessed id error = %v, want ErrUnknownExport", err)
	} else if !IsDisconnectRequired(err) {
		t.Fatalf("guessed id must require disconnect: %v", err)
	}
	if !attacker.IsClosed() {
		t.Fatal("dispatcher did not close attacker table after forged target")
	}
	if victimCap.callCount() != 0 {
		t.Fatal("guessed id reached a capability")
	}
}

func TestCrossConnectionTargetAndTableMismatchDisconnect(t *testing.T) {
	ctx := context.Background()
	connA := NewExportTable("A")
	capA := &recordingCapability{}
	idA, err := connA.Export(capA, 1)
	if err != nil {
		t.Fatalf("export A: %v", err)
	}

	connB := NewExportTable("B")
	capB := &recordingCapability{}
	idB, err := connB.Export(capB, 1)
	if err != nil {
		t.Fatalf("export B: %v", err)
	}

	// A numeric collision is fine: each table resolves the handle to the
	// capability it owns, never to the other connection's object.
	if idA != idB {
		t.Fatalf("test setup ids differ: %d != %d", idA, idB)
	}
	if _, err := connB.Dispatch(ctx, Call{Target: idB, Method: 1}); err != nil {
		t.Fatalf("conn B own export: %v", err)
	}
	if capA.callCount() != 0 || capB.callCount() != 1 {
		t.Fatalf("cross-connection collision leaked: A=%d B=%d", capA.callCount(), capB.callCount())
	}

	// A dispatcher routing conn B traffic through conn A's table is a
	// forged cross-connection reference, not an ordinary lookup miss.
	if _, err := connA.DispatchFrom(ctx, "B", Call{Target: idA, Method: 1}); !errors.Is(err, ErrConnectionMismatch) {
		t.Fatalf("mismatch error = %v, want ErrConnectionMismatch", err)
	} else if !IsDisconnectRequired(err) {
		t.Fatalf("mismatch must require disconnect: %v", err)
	}
	if !connA.IsClosed() {
		t.Fatal("mismatched connection table was not closed")
	}
}

func TestOverReleaseIsRejectedWithoutChangingRefCount(t *testing.T) {
	table := NewExportTable("conn-1")
	cap := &recordingCapability{}
	id, err := table.Export(cap, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := table.Retain(id, 2); err != nil {
		t.Fatalf("retain: %v", err)
	}

	remaining, err := table.ReleaseAndRemaining(id, 4)
	if !errors.Is(err, ErrReleaseOverflow) {
		t.Fatalf("over-release error = %v, want ErrReleaseOverflow", err)
	}
	if IsDisconnectRequired(err) {
		t.Fatal("ordinary over-release must not be classified as forged target")
	}
	if remaining != 3 {
		t.Fatalf("remaining = %d, want 3", remaining)
	}
	if refs, err := table.RefCount(id); err != nil || refs != 3 {
		t.Fatalf("refcount after rejected release = %d, %v; want 3, nil", refs, err)
	}
	if _, err := table.Resolve(id, 1); err != nil {
		t.Fatalf("export became unusable after rejected release: %v", err)
	}

	if err := table.Release(id, 3); err != nil {
		t.Fatalf("final release: %v", err)
	}
	if _, err := table.RefCount(id); !errors.Is(err, ErrUnknownExport) {
		t.Fatalf("refcount after final release = %v, want ErrUnknownExport", err)
	}
	if !table.IsReleased(id) {
		t.Fatal("released id is not pending peer confirmation")
	}

	// A duplicate release after zero must also be rejected; it must not
	// underflow the counter or resurrect the export.
	if err := table.Release(id, 1); !errors.Is(err, ErrReleaseOverflow) {
		t.Fatalf("duplicate release error = %v, want ErrReleaseOverflow", err)
	}
	if _, err := table.Resolve(id, 1); !errors.Is(err, ErrUnknownExport) {
		t.Fatalf("released export resolved: %v", err)
	}
}

func TestZeroReleaseCountRejected(t *testing.T) {
	table := NewExportTable("conn-1")
	cap := &recordingCapability{}
	id, err := table.Export(cap, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := table.Release(id, 0); !errors.Is(err, ErrInvalidReleaseCount) {
		t.Fatalf("zero release error = %v, want ErrInvalidReleaseCount", err)
	}
	if err := table.Retain(id, 0); !errors.Is(err, ErrInvalidReleaseCount) {
		t.Fatalf("zero retain error = %v, want ErrInvalidReleaseCount", err)
	}
	if refs, err := table.RefCount(id); err != nil || refs != 1 {
		t.Fatalf("refcount changed by zero release: %d, %v", refs, err)
	}
}

func TestConcurrentReleasesCannotUnderflow(t *testing.T) {
	table := NewExportTable("conn-1")
	cap := &recordingCapability{}
	id, err := table.Export(cap, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	const refs = 20
	if err := table.Retain(id, refs-1); err != nil {
		t.Fatalf("retain: %v", err)
	}

	var successes atomic.Int32
	var overflow atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < refs*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := table.Release(id, 1)
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrReleaseOverflow):
				overflow.Add(1)
			default:
				t.Errorf("unexpected release error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != refs {
		t.Fatalf("successful releases = %d, want %d", got, refs)
	}
	if got := overflow.Load(); got != refs {
		t.Fatalf("rejected releases = %d, want %d", got, refs)
	}
	if _, err := table.RefCount(id); !errors.Is(err, ErrUnknownExport) {
		t.Fatalf("refcount after concurrent releases = %v", err)
	}
	if !table.IsReleased(id) {
		t.Fatal("id not pending confirmation after concurrent releases")
	}
}

func TestReleasedIDStaysQuarantinedUntilConfirmation(t *testing.T) {
	table := NewExportTable("conn-1")
	oldCap := &recordingCapability{}
	id, err := table.Export(oldCap, 1)
	if err != nil {
		t.Fatalf("export old: %v", err)
	}
	if err := table.Release(id, 1); err != nil {
		t.Fatalf("release old: %v", err)
	}

	// Reusing the ID now would create an ABA hazard: a delayed call for the
	// old reference could hit the replacement object.
	replacement := &recordingCapability{}
	id2, err := table.Export(replacement, 2)
	if err != nil {
		t.Fatalf("export replacement: %v", err)
	}
	if id2 == id {
		t.Fatalf("released id %d was reallocated before peer confirmation", id)
	}
	if _, err := table.Resolve(id, 1); !errors.Is(err, ErrUnknownExport) {
		t.Fatalf("quarantined id resolved: %v", err)
	}
	if !table.IsReleased(id) {
		t.Fatal("released id lost its quarantine marker")
	}

	if err := table.ConfirmRelease(id); err != nil {
		t.Fatalf("confirm release: %v", err)
	}
	if table.IsReleased(id) {
		t.Fatal("confirmed id is still quarantined")
	}

	id3, err := table.Export(replacement, 2)
	if err != nil {
		t.Fatalf("export after confirmation: %v", err)
	}
	if id3 != id {
		t.Fatalf("confirmed id %d not reused: got %d", id, id3)
	}
	if _, err := table.Invoke(context.Background(), id3, 2, nil); err != nil {
		t.Fatalf("invoke reused id: %v", err)
	}
	if oldCap.callCount() != 0 {
		t.Fatal("stale handle reached the released old capability")
	}
}

func TestConfirmRequiresAnActualRelease(t *testing.T) {
	table := NewExportTable("conn-1")
	cap := &recordingCapability{}
	id, err := table.Export(cap, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := table.ConfirmRelease(id); !errors.Is(err, ErrNoPendingRelease) {
		t.Fatalf("confirm active export error = %v, want ErrNoPendingRelease", err)
	}
	if _, err := table.Resolve(id, 1); err != nil {
		t.Fatalf("active export was disturbed: %v", err)
	}

	if err := table.Release(id, 1); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := table.ConfirmRelease(id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := table.ConfirmRelease(id); !errors.Is(err, ErrNoPendingRelease) {
		t.Fatalf("double confirm error = %v, want ErrNoPendingRelease", err)
	}
}

func TestAttenuationEnforcesCallerMethodWhitelist(t *testing.T) {
	ctx := context.Background()
	table := NewExportTable("conn-1")
	target := &recordingCapability{}

	id, err := table.Export(target, 1, 3)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	res, err := table.Invoke(ctx, id, 2, nil)
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("unlisted method error = %v, want ErrMethodNotAllowed", err)
	}
	if res != nil {
		t.Fatalf("unlisted method returned result %q", res)
	}
	if IsDisconnectRequired(err) {
		t.Fatal("method rejection must not disconnect the connection")
	}
	if target.callCount() != 0 {
		t.Fatal("unlisted method reached the target")
	}

	res, err = table.Invoke(ctx, id, 1, nil)
	if err != nil {
		t.Fatalf("whitelisted method: %v", err)
	}
	if string(res) != "method-1" {
		t.Fatalf("result = %q", res)
	}
	if target.callCount() != 1 {
		t.Fatalf("target calls = %d, want 1", target.callCount())
	}

	view, err := table.Resolve(id, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := view.MethodWhitelist(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("whitelist = %v, want [1 3]", got)
	}
}

func TestEmptyWhitelistAndExportAll(t *testing.T) {
	ctx := context.Background()
	table := NewExportTable("conn-1")
	target := &recordingCapability{}

	deniedID, err := table.Export(target)
	if err != nil {
		t.Fatalf("export deny: %v", err)
	}
	if _, err := table.Invoke(ctx, deniedID, 0, nil); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("empty whitelist allowed method 0: %v", err)
	}
	if target.callCount() != 0 {
		t.Fatal("deny-by-default export reached target")
	}

	allID, err := table.ExportAll(target)
	if err != nil {
		t.Fatalf("export all: %v", err)
	}
	for _, method := range []MethodID{0, 1, 2} {
		if _, err := table.Invoke(ctx, allID, method, nil); err != nil {
			t.Fatalf("ExportAll method %d: %v", method, err)
		}
	}
	if target.callCount() != 3 {
		t.Fatalf("target calls = %d, want 3", target.callCount())
	}
}

func TestWhitelistCannotBeWidenedThroughExportFields(t *testing.T) {
	ctx := context.Background()
	table := NewExportTable("conn-1")
	target := &recordingCapability{}
	id, err := table.Export(target, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	view, err := table.Resolve(id, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Export fields are audit metadata, not the access-control decision.
	view.Methods = append(view.Methods, 2)
	view.AllMethods = true
	if _, err := view.Call(ctx, 2, nil); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("mutating exported metadata widened the view: %v", err)
	}
	if got := view.MethodWhitelist(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("authoritative whitelist changed through metadata: %v", got)
	}
	if target.callCount() != 0 {
		t.Fatal("widened metadata reached target")
	}
}

func TestNilCapabilityRejected(t *testing.T) {
	table := NewExportTable("conn-1")
	if _, err := table.Export(nil, 1); !errors.Is(err, ErrNilCapability) {
		t.Fatalf("nil export error = %v, want ErrNilCapability", err)
	}
	var typedNil *recordingCapability
	if _, err := table.Export(typedNil, 1); !errors.Is(err, ErrNilCapability) {
		t.Fatalf("typed nil export error = %v, want ErrNilCapability", err)
	}
}

func TestAttenuationComposesNarrower(t *testing.T) {
	ctx := context.Background()
	target := &recordingCapability{}
	inner := Attenuate(target, 1, 2)
	outer := Attenuate(inner, 2, 3)

	if _, err := outer.Call(ctx, 1, nil); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("inner-only method escaped outer attenuation: %v", err)
	}
	if _, err := outer.Call(ctx, 3, nil); !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("outer-only method escaped inner attenuation: %v", err)
	}
	if _, err := outer.Call(ctx, 2, nil); err != nil {
		t.Fatalf("intersection method: %v", err)
	}
	if got := outer.(*AttenuatedCapability).Methods(); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("outer view methods = %v, want [2 3]", got)
	}
}

func TestCloseDisconnectsAllExports(t *testing.T) {
	table := NewExportTable("conn-1")
	cap := &recordingCapability{}
	id, err := table.Export(cap, 1)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	view, err := table.Resolve(id, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	table.Close()
	if !table.IsClosed() {
		t.Fatal("table not marked closed")
	}
	if _, err := view.Call(context.Background(), 1, nil); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("view invoked after close = %v, want ErrTableClosed", err)
	}
	if _, err := table.Invoke(context.Background(), id, 1, nil); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("invoke after close = %v, want ErrTableClosed", err)
	}
	if _, err := table.Export(cap, 1); !errors.Is(err, ErrTableClosed) {
		t.Fatalf("export after close = %v, want ErrTableClosed", err)
	}
}
