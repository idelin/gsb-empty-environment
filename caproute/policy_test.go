package caproute

import (
	"bytes"
	"context"
	"testing"
)

func TestEntryHappyPathAndOpaqueHandle(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "secret-doc"); err != nil {
		t.Fatal(err)
	}
	h := mint(t, host, "alpha", TypeFile, "secret-doc", []Operation{OpFileRead, OpFileStat})

	out, err := host.Entry(context.Background(), "alpha", h, OpFileRead, []byte("payload"))
	if err != nil {
		t.Fatalf("entry failed: %v", err)
	}
	if string(out) != "ok:"+string(OpFileRead) {
		t.Fatalf("unexpected response %q", out)
	}
	if calls, _ := res.snapshot(); calls != 1 {
		t.Fatalf("expected 1 callback, got %d", calls)
	}

	// Handles are opaque random tokens and must not embed identity.
	if bytes.Contains(h, []byte("secret")) || bytes.Contains(h, []byte("alpha")) || bytes.Contains(h, []byte("file")) {
		t.Fatalf("handle leaks identity: %x", h)
	}
}

func TestCrossTypeReinterpretation(t *testing.T) {
	host, _ := setupHost(t)
	fileRes := newTrackResource(false)
	netRes := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, fileRes, "doc"); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterResource(TypeNetwork, netRes, "socket"); err != nil {
		t.Fatal(err)
	}
	fileHandle := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	netHandle := mint(t, host, "alpha", TypeNetwork, "socket", []Operation{OpNetDial})

	// A file capability cannot be driven through a network operation.
	_, err := host.Entry(context.Background(), "alpha", fileHandle, OpNetDial, nil)
	if got := denialReason(t, err); got != ReasonTypeMismatch {
		t.Fatalf("file handle + net op: got reason %s", got)
	}
	// And vice versa.
	_, err = host.Entry(context.Background(), "alpha", netHandle, OpFileWrite, nil)
	if got := denialReason(t, err); got != ReasonTypeMismatch {
		t.Fatalf("net handle + file op: got reason %s", got)
	}
	// Unknown operations are rejected before any resource work.
	_, err = host.Entry(context.Background(), "alpha", fileHandle, Operation("kernel.escalate"), nil)
	if got := denialReason(t, err); got != ReasonUnknownOperation {
		t.Fatalf("unknown op: got reason %s", got)
	}
	// Minting itself must refuse cross-type operation sets.
	_, err = host.MintCapability("alpha", TypeFile, "doc", []Operation{OpNetSend})
	if got := denialReason(t, err); got != ReasonTypeMismatch {
		t.Fatalf("mint cross-type: got reason %s", got)
	}
	if calls, _ := fileRes.snapshot(); calls != 0 {
		t.Fatalf("file resource must never be entered, calls=%d", calls)
	}
	if calls, _ := netRes.snapshot(); calls != 0 {
		t.Fatalf("net resource must never be entered, calls=%d", calls)
	}
}

func TestArbitraryBytesAndForeignHandles(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	alphaHandle := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})

	bogus := [][]byte{nil, {}, []byte("not-a-handle"), bytes.Repeat([]byte{0x00}, len(alphaHandle)), bytes.Repeat([]byte{0xff}, len(alphaHandle))}
	for _, b := range bogus {
		_, err := host.Entry(context.Background(), "alpha", b, OpFileRead, nil)
		if got := denialReason(t, err); got != ReasonInvalidHandle {
			t.Fatalf("bogus handle %x: got reason %s", b, got)
		}
	}

	// A handle minted for alpha must be unusable by beta, with the same
	// opaque error string as a forged token so probing cannot distinguish.
	_, foreignErr := host.Entry(context.Background(), "beta", alphaHandle, OpFileRead, nil)
	_, forgedErr := host.Entry(context.Background(), "beta", []byte("totally-forged"), OpFileRead, nil)
	if denialReason(t, foreignErr) != ReasonInvalidHandle || denialReason(t, forgedErr) != ReasonInvalidHandle {
		t.Fatal("foreign and forged handles must share the invalid_handle reason")
	}
	if foreignErr.Error() != forgedErr.Error() {
		t.Fatalf("error strings must be identical to avoid probing: %q vs %q", foreignErr, forgedErr)
	}
	if calls, _ := res.snapshot(); calls != 0 {
		t.Fatalf("resource must not be entered, calls=%d", calls)
	}
}

func TestDelegationCanOnlyNarrow(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	parent := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead, OpFileStat})

	readOnly, err := host.Delegate("alpha", parent, []Operation{OpFileRead})
	if err != nil {
		t.Fatalf("narrow delegation failed: %v", err)
	}
	if _, err := host.Entry(context.Background(), "alpha", readOnly, OpFileRead, nil); err != nil {
		t.Fatalf("delegated read failed: %v", err)
	}

	// The narrowed handle cannot perform an operation it dropped.
	_, err = host.Entry(context.Background(), "alpha", readOnly, OpFileStat, nil)
	if got := denialReason(t, err); got != ReasonOperationDenied {
		t.Fatalf("dropped op: got reason %s", got)
	}

	// Requesting an operation the parent lacks is widening.
	_, err = host.Delegate("alpha", parent, []Operation{OpFileRead, OpFileWrite})
	if got := denialReason(t, err); got != ReasonWidening {
		t.Fatalf("widening delegation: got reason %s", got)
	}
	// Empty delegations are rejected too.
	_, err = host.Delegate("alpha", parent, nil)
	if got := denialReason(t, err); got != ReasonWidening {
		t.Fatalf("empty delegation: got reason %s", got)
	}
}

func TestProxyIntersectionWashesNoPrivilege(t *testing.T) {
	host, _ := setupHost(t)
	shared := newTrackResource(false)
	if err := host.RegisterResource(TypeNetwork, shared, "bus"); err != nil {
		t.Fatal(err)
	}

	// alpha holds only send; beta holds send+receive on the same resource.
	alphaHandle := mint(t, host, "alpha", TypeNetwork, "bus", []Operation{OpNetSend})
	betaHandle := mint(t, host, "beta", TypeNetwork, "bus", []Operation{OpNetSend, OpNetReceive})

	// alpha enters (send only), then beta proxies with its stronger handle.
	// The intersection still excludes receive.
	chain := singleChain("alpha", OpNetSend)
	_, err := host.Proxy(context.Background(), "beta", betaHandle, OpNetReceive, nil, chain)
	if got := denialReason(t, err); got != ReasonOperationDenied {
		t.Fatalf("privilege laundering: got reason %s", got)
	}

	// The operation shared by every frame still passes.
	if _, err := host.Proxy(context.Background(), "beta", betaHandle, OpNetSend, []byte("hi"), chain); err != nil {
		t.Fatalf("shared operation should pass the intersection: %v", err)
	}

	// A proxy call without a chain is rejected outright.
	_, err = host.Proxy(context.Background(), "alpha", alphaHandle, OpNetSend, nil, nil)
	if got := denialReason(t, err); got != ReasonNoCallChain {
		t.Fatalf("chainless proxy: got reason %s", got)
	}

	if calls, _ := shared.snapshot(); calls != 1 {
		t.Fatalf("only the shared op should reach the resource, calls=%d", calls)
	}
}

func TestProxyCycleRejected(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}
	alphaHandle := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	betaHandle := mint(t, host, "beta", TypeFile, "doc", []Operation{OpFileRead})

	chain := CallChain{
		{Instance: "alpha", Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)},
		{Instance: "beta", Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)},
	}
	// alpha already appears: routing back to it is a cycle.
	_, err := host.Proxy(context.Background(), "alpha", alphaHandle, OpFileRead, nil, chain)
	if got := denialReason(t, err); got != ReasonChainCycle {
		t.Fatalf("cycle back to alpha: got reason %s", got)
	}
	// A fresh hop to beta from an alpha-only chain is legal.
	shortChain := singleChain("alpha", OpFileRead)
	if _, err := host.Proxy(context.Background(), "beta", betaHandle, OpFileRead, nil, shortChain); err != nil {
		t.Fatalf("non-cyclic proxy failed: %v", err)
	}
	if calls, _ := res.snapshot(); calls != 1 {
		t.Fatalf("only the legal hop should reach the resource, calls=%d", calls)
	}
}

func TestMaxChainDepth(t *testing.T) {
	host, _ := setupHost(t)
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}

	const n = MaxChainDepth + 1
	handles := make([]Handle, n)
	for i := 0; i < n; i++ {
		id := InstanceID("inst-" + itoa(i))
		if err := host.RegisterInstance(id); err != nil {
			t.Fatal(err)
		}
		handles[i] = mint(t, host, id, TypeFile, "doc", []Operation{OpFileRead})
	}

	// A chain already at the limit cannot be extended.
	full := make(CallChain, MaxChainDepth)
	for i := range full {
		full[i] = ChainFrame{Instance: InstanceID("chain-" + itoa(i)), Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)}
	}
	_, err := host.Proxy(context.Background(), "inst-00", handles[0], OpFileRead, nil, full)
	if got := denialReason(t, err); got != ReasonChainDepth {
		t.Fatalf("depth overflow: got reason %s", got)
	}

	// 32 legal nested hops succeed; the 33rd is refused.
	chain := singleChain("inst-00", OpFileRead)
	for i := 1; i < MaxChainDepth; i++ {
		id := InstanceID("inst-" + itoa(i))
		out, err := host.Proxy(context.Background(), id, handles[i], OpFileRead, nil, chain)
		if err != nil {
			t.Fatalf("hop %d should be allowed: %v", i, err)
		}
		_ = out
		chain = append(chain, ChainFrame{Instance: id, Allowed: []Operation{OpFileRead}, allowed: opSetOf(OpFileRead)})
	}
	_, err = host.Proxy(context.Background(), "inst-32", handles[MaxChainDepth], OpFileRead, nil, chain)
	if got := denialReason(t, err); got != ReasonChainDepth {
		t.Fatalf("33rd hop: got reason %s", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "00"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if len(b) == 1 {
		b = append([]byte{'0'}, b...)
	}
	return string(b)
}
