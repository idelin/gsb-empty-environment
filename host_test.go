package netsec

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func denyReason(t *testing.T, err error) DenyReason {
	t.Helper()
	var derr *Error
	if !errors.As(err, &derr) {
		t.Fatalf("expected *Error, got %v", err)
	}
	return derr.Reason
}

func mustOpen(t *testing.T, h *Host, inst InstanceID, rt ResourceType, ops Op, name string) (*Resource, Handle) {
	t.Helper()
	res, handle, err := h.OpenResource(inst, rt, ops, name)
	if err != nil {
		t.Fatalf("OpenResource: %v", err)
	}
	return res, handle
}

func TestCrossTypeReinterpretation(t *testing.T) {
	h := NewHost()
	inst := h.RegisterInstance()
	_, handle := mustOpen(t, h, inst, ResourceFile, OpRead|OpWrite, "tenant-file")

	// Flip the resource type byte: file handle reinterpreted as network.
	forged := handle
	forged[25] = byte(ResourceNetwork)
	err := h.Enter(forged, OpRead, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyTamperedHandle {
		t.Fatalf("reinterpreted type: got %v, want %v", got, DenyTamperedHandle)
	}

	// Flip operation bits to self-grant OpConnect.
	forged = handle
	forged[26] |= byte(OpConnect >> 24)
	forged[29] |= byte(OpConnect)
	err = h.Enter(forged, OpConnect, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyTamperedHandle {
		t.Fatalf("forged ops: got %v, want %v", got, DenyTamperedHandle)
	}

	// Random bytes are not a capability at all.
	var garbage Handle
	for i := range garbage {
		garbage[i] = byte(i*31 + 7)
	}
	garbage[0] = handle[0]
	err = h.Enter(garbage, OpRead, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyUnknownHandle {
		t.Fatalf("garbage handle: got %v, want %v", got, DenyUnknownHandle)
	}

	// The untouched handle still works.
	if err := h.Enter(handle, OpRead, nil, func() error { return nil }); err != nil {
		t.Fatalf("honest handle rejected: %v", err)
	}
}

func TestDelegationCannotWiden(t *testing.T) {
	h := NewHost()
	a := h.RegisterInstance()
	b := h.RegisterInstance()
	_, handle := mustOpen(t, h, a, ResourceFile, OpRead, "a-file")

	// Widening delegation must be refused.
	if _, err := h.Delegate(handle, nil, b, OpRead|OpWrite); true {
		if got := denyReason(t, err); got != DenyDelegationWidens {
			t.Fatalf("widening delegate: got %v, want %v", got, DenyDelegationWidens)
		}
	}

	// Narrowing delegation succeeds and the delegatee is bound by it.
	narrow, err := h.Delegate(handle, nil, b, OpRead)
	if err != nil {
		t.Fatalf("narrowing delegate: %v", err)
	}
	if err := h.Enter(narrow, OpRead, nil, func() error { return nil }); err != nil {
		t.Fatalf("delegated read: %v", err)
	}
	if err := h.Enter(narrow, OpWrite, nil, func() error { return nil }); true {
		if got := denyReason(t, err); got != DenyOpNotAllowed {
			t.Fatalf("delegated write: got %v, want %v", got, DenyOpNotAllowed)
		}
	}

	// Re-delegation stays narrowing even across a second hop.
	c := h.RegisterInstance()
	if _, err := h.Delegate(narrow, nil, c, OpRead|OpConnect); true {
		if got := denyReason(t, err); got != DenyDelegationWidens {
			t.Fatalf("second-hop widening: got %v, want %v", got, DenyDelegationWidens)
		}
	}
}

func TestProxyCannotLaunderPrivileges(t *testing.T) {
	h := NewHost()
	a := h.RegisterInstance()
	b := h.RegisterInstance()
	_, aHandle := mustOpen(t, h, a, ResourceFile, OpRead, "a-readonly")
	_, bHandle := mustOpen(t, h, b, ResourceNetwork, OpRead|OpWrite|OpConnect, "b-wide")

	// A's call proxied through the highly privileged B: the effective set
	// is the intersection of the whole chain, so B cannot amplify it.
	chain := []Link{
		{Instance: a, Ops: OpRead},
		{Instance: b, Ops: OpRead | OpWrite | OpConnect},
	}
	if err := h.Enter(aHandle, OpRead, chain, func() error { return nil }); err != nil {
		t.Fatalf("read through proxy: %v", err)
	}
	err := h.Enter(aHandle, OpWrite, chain, func() error { return nil })
	if got := denyReason(t, err); got != DenyOpNotAllowed {
		t.Fatalf("write through proxy: got %v, want %v", got, DenyOpNotAllowed)
	}

	// B cannot spend its own wide handle while presenting A's chain head.
	swapped := []Link{
		{Instance: a, Ops: OpRead},
		{Instance: b, Ops: OpRead | OpWrite},
	}
	err = h.Enter(bHandle, OpWrite, swapped, func() error { return nil })
	if got := denyReason(t, err); got != DenyForeignHandle {
		t.Fatalf("laundered handle: got %v, want %v", got, DenyForeignHandle)
	}

	// Nor can B widen A's capability by delegation.
	if _, err := h.Delegate(aHandle, chain, b, OpWrite); true {
		if got := denyReason(t, err); got != DenyDelegationWidens {
			t.Fatalf("proxy delegation widening: got %v, want %v", got, DenyDelegationWidens)
		}
	}
}

func TestStaleGenerationAcrossUnloadReload(t *testing.T) {
	h := NewHost()
	inst := h.RegisterInstance()
	_, oldHandle := mustOpen(t, h, inst, ResourceFile, OpRead, "gen1-file")

	if err := h.UnloadInstance(inst); err != nil {
		t.Fatalf("unload: %v", err)
	}
	err := h.Enter(oldHandle, OpRead, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyInstanceUnloaded {
		t.Fatalf("unloaded enter: got %v, want %v", got, DenyInstanceUnloaded)
	}

	if err := h.ReloadInstance(inst); err != nil {
		t.Fatalf("reload: %v", err)
	}
	err = h.Enter(oldHandle, OpRead, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyStaleGeneration {
		t.Fatalf("stale handle after reload: got %v, want %v", got, DenyStaleGeneration)
	}

	// Fresh handles minted after reload work.
	_, newHandle := mustOpen(t, h, inst, ResourceFile, OpRead, "gen2-file")
	if err := h.Enter(newHandle, OpRead, nil, func() error { return nil }); err != nil {
		t.Fatalf("new generation handle: %v", err)
	}
}

func TestGenerationNeverWraps(t *testing.T) {
	h := NewHost()
	inst := h.RegisterInstance()
	mustOpen(t, h, inst, ResourceFile, OpRead, "last-gen")

	// Force the instance to the edge of the generation space.
	h.mu.Lock()
	h.instances[inst].generation = math.MaxUint64
	h.mu.Unlock()

	_, handle := mustOpen(t, h, inst, ResourceFile, OpRead, "edge-gen")
	if err := h.Enter(handle, OpRead, nil, func() error { return nil }); err != nil {
		t.Fatalf("max-generation handle should still work: %v", err)
	}
	if err := h.UnloadInstance(inst); err != nil {
		t.Fatalf("unload at max generation: %v", err)
	}
	if err := h.ReloadInstance(inst); err == nil {
		t.Fatal("reload of a retired instance must fail")
	}
	err := h.Enter(handle, OpRead, nil, func() error { return nil })
	if got := denyReason(t, err); got != DenyInstanceRetired {
		t.Fatalf("retired instance: got %v, want %v", got, DenyInstanceRetired)
	}
}

func TestConcurrentClose(t *testing.T) {
	h := NewHost()
	inst := h.RegisterInstance()
	res, handle := mustOpen(t, h, inst, ResourceFile, OpRead, "hot-file")

	const workers = 16
	var entered, completed atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.Enter(handle, OpRead, nil, func() error {
				entered.Add(1)
				<-release
				completed.Add(1)
				return nil
			})
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for entered.Load() < workers {
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d callbacks entered", entered.Load(), workers)
		}
		time.Sleep(time.Millisecond)
	}

	closed := make(chan struct{})
	go func() {
		res.Close()
		close(closed)
	}()
	// Close must wait for the leased callbacks to finish.
	select {
	case <-closed:
		t.Fatal("Close returned while leases were outstanding")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	<-closed
	if completed.Load() != workers {
		t.Fatalf("completed %d/%d leased callbacks", completed.Load(), workers)
	}

	// After Close returned, no new callback may enter the resource.
	ran := false
	err := h.Enter(handle, OpRead, nil, func() error {
		ran = true
		return nil
	})
	if ran {
		t.Fatal("callback ran after Close returned")
	}
	if got := denyReason(t, err); got != DenyResourceClosed {
		t.Fatalf("post-close enter: got %v, want %v", got, DenyResourceClosed)
	}
}

func TestCycleAndDepthLimits(t *testing.T) {
	h := NewHost()
	a := h.RegisterInstance()
	b := h.RegisterInstance()
	_, handle := mustOpen(t, h, a, ResourceFile, OpRead, "chain-file")

	cyclic := []Link{
		{Instance: a, Ops: OpRead},
		{Instance: b, Ops: OpRead},
		{Instance: a, Ops: OpRead},
	}
	err := h.Enter(handle, OpRead, cyclic, func() error { return nil })
	if got := denyReason(t, err); got != DenyCycleDetected {
		t.Fatalf("cyclic chain: got %v, want %v", got, DenyCycleDetected)
	}
	if _, err := h.Delegate(handle, cyclic[:2], a, OpRead); true {
		if got := denyReason(t, err); got != DenyCycleDetected {
			t.Fatalf("delegation closing a cycle: got %v, want %v", got, DenyCycleDetected)
		}
	}

	deep := make([]Link, MaxChainDepth+1)
	for i := range deep {
		deep[i] = Link{Instance: h.RegisterInstance(), Ops: OpRead}
	}
	deep[0].Instance = a
	err = h.Enter(handle, OpRead, deep, func() error { return nil })
	if got := denyReason(t, err); got != DenyDepthExceeded {
		t.Fatalf("over-deep chain: got %v, want %v", got, DenyDepthExceeded)
	}

	atMax := deep[:MaxChainDepth]
	if _, err := h.Delegate(handle, atMax, h.RegisterInstance(), OpRead); true {
		if got := denyReason(t, err); got != DenyDepthExceeded {
			t.Fatalf("delegation past max depth: got %v, want %v", got, DenyDepthExceeded)
		}
	}
}

func TestDenialLogHidesOtherTenants(t *testing.T) {
	h := NewHost()
	a := h.RegisterInstance()
	b := h.RegisterInstance()
	const secretName = "tenant-a-ultra-secret-object"
	_, aHandle := mustOpen(t, h, a, ResourceFile, OpRead, secretName)

	// B presents A's handle as its own.
	chain := []Link{{Instance: b, Ops: OpRead}}
	err := h.Enter(aHandle, OpRead, chain, func() error { return nil })
	if got := denyReason(t, err); got != DenyForeignHandle {
		t.Fatalf("foreign handle: got %v, want %v", got, DenyForeignHandle)
	}
	if strings.Contains(err.Error(), secretName) {
		t.Fatalf("error leaks resource identity: %q", err.Error())
	}

	denials := h.Denials()
	if len(denials) == 0 {
		t.Fatal("denial was not recorded")
	}
	last := denials[len(denials)-1]
	if last.Reason != DenyForeignHandle {
		t.Fatalf("logged reason %v, want %v", last.Reason, DenyForeignHandle)
	}
	if last.Requester != b {
		t.Fatal("denial should be attributed to the requesting instance")
	}
}
