package dma

import (
	"errors"
	"testing"
)

func newTestChain(t *testing.T, descs []Descriptor) *FrozenChain {
	t.Helper()
	g := newFakeGuest(1)
	for i, d := range descs {
		writeDesc(g, testRingBase, i, d)
	}
	chain, err := NewFreezer(nil, 0).Freeze(g, testRingBase, len(descs), 0)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	return chain
}

func twoDescChain(t *testing.T) *FrozenChain {
	return newTestChain(t, []Descriptor{
		{Addr: 0x2000, Length: 64, Next: 1, Flags: FlagRead},
		{Addr: 0x4000, Length: 32, Next: 0, Flags: FlagWrite | FlagEnd},
	})
}

func TestExecutorSubmitAndComplete(t *testing.T) {
	pins := NewPinSet()
	cb := &recordingCallback{}
	exec := NewExecutor(cb, pins)
	chain := twoDescChain(t)

	gen, err := exec.Submit(chain)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if gen != 1 || chain.Generation() != 1 {
		t.Fatalf("generation binding mismatch: gen=%d chain=%d", gen, chain.Generation())
	}
	if exec.InFlight() != 2 {
		t.Fatalf("want 2 in-flight, got %d", exec.InFlight())
	}
	if len(cb.chains) != 1 || cb.chains[0] != chain {
		t.Fatal("callback must receive the private frozen chain once")
	}
	if pins.Count(0x2000) != 1 || pins.Count(0x4000) != 1 {
		t.Fatal("pages must be pinned while in flight")
	}

	if err := exec.Complete(Receipt{Generation: gen, Slot: 0}); err != nil {
		t.Fatalf("complete slot 0: %v", err)
	}
	if pins.Count(0x2000) != 0 {
		t.Fatal("completed page must be unpinned")
	}
	if pins.Count(0x4000) != 1 {
		t.Fatal("unrelated page must stay pinned")
	}

	// 重复完成同一项被拒绝，且不得再次回收。
	if err := exec.Complete(Receipt{Generation: gen, Slot: 0}); !errors.Is(err, ErrStaleCompletion) {
		t.Fatalf("duplicate completion: want ErrStaleCompletion, got %v", err)
	}
	if err := exec.Complete(Receipt{Generation: gen, Slot: 9}); !errors.Is(err, ErrStaleCompletion) {
		t.Fatalf("unknown slot: want ErrStaleCompletion, got %v", err)
	}

	if err := exec.Complete(Receipt{Generation: gen, Slot: 1}); err != nil {
		t.Fatalf("complete slot 1: %v", err)
	}
	if exec.InFlight() != 0 || pins.Count(0x4000) != 0 {
		t.Fatal("all pages must be released after completion")
	}
}

func TestExecutorOldGenerationRejected(t *testing.T) {
	pins := NewPinSet()
	exec := NewExecutor(DeviceCallbackFunc(func(*FrozenChain) error { return nil }), pins)

	first := twoDescChain(t)
	gen1, err := exec.Submit(first)
	if err != nil {
		t.Fatal(err)
	}
	// 第一代全部正常完成并回收。
	if err := exec.Complete(Receipt{Generation: gen1, Slot: 0}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Complete(Receipt{Generation: gen1, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	second := twoDescChain(t)
	gen2, err := exec.Submit(second)
	if err != nil {
		t.Fatal(err)
	}
	if gen2 <= gen1 {
		t.Fatalf("generations must advance: %d -> %d", gen1, gen2)
	}

	// 第一代已完成，其任何旧回执（即使槽位在新一代重新出现）都必须拒绝，
	// 且不能误回收第二代的在途页。
	if err := exec.Complete(Receipt{Generation: gen1, Slot: 0}); !errors.Is(err, ErrStaleCompletion) {
		t.Fatalf("stale gen1 receipt: want ErrStaleCompletion, got %v", err)
	}
	if pins.Count(0x2000) != 1 {
		t.Fatal("stale receipt must not release the new generation's page")
	}
	// 伪造的未知代次同样拒绝。
	if err := exec.Complete(Receipt{Generation: 999, Slot: 0}); !errors.Is(err, ErrStaleCompletion) {
		t.Fatalf("forged generation: want ErrStaleCompletion, got %v", err)
	}
	// 第二代的合法回执仍可正常回收。
	if err := exec.Complete(Receipt{Generation: gen2, Slot: 0}); err != nil {
		t.Fatalf("current generation completion: %v", err)
	}
}

func TestExecutorCallbackFailureKeepsPagesPinned(t *testing.T) {
	pins := NewPinSet()
	boom := errors.New("device busy")
	exec := NewExecutor(DeviceCallbackFunc(func(*FrozenChain) error { return boom }), pins)

	gen, err := exec.Submit(twoDescChain(t))
	if !errors.Is(err, ErrCallbackFailed) {
		t.Fatalf("want wrapped ErrCallbackFailed, got %v", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("underlying cause must be preserved, got %v", err)
	}
	if exec.InFlight() != 2 {
		t.Fatalf("failed submission must stay in flight, got %d", exec.InFlight())
	}
	if pins.Count(0x2000) != 1 || pins.Count(0x4000) != 1 {
		t.Fatal("pages must remain pinned after callback failure")
	}

	// 回调失败不影响逐项完成回收。
	if err := exec.Complete(Receipt{Generation: gen, Slot: 0}); err != nil {
		t.Fatalf("completion after failed callback: %v", err)
	}
	if pins.Count(0x2000) != 0 || pins.Count(0x4000) != 1 {
		t.Fatal("only the completed item must be released")
	}
}

func TestExecutorSharedPageRefcount(t *testing.T) {
	pins := NewPinSet()
	exec := NewExecutor(DeviceCallbackFunc(func(*FrozenChain) error { return nil }), pins)
	chain := newTestChain(t, []Descriptor{
		// 两个描述符都落在页基址 0x2000 内。
		{Addr: 0x2010, Length: 16, Next: 1, Flags: FlagRead},
		{Addr: 0x2080, Length: 16, Next: 0, Flags: FlagRead | FlagEnd},
	})
	gen, err := exec.Submit(chain)
	if err != nil {
		t.Fatal(err)
	}
	if pins.Count(0x2000) != 2 {
		t.Fatalf("shared page refcount must be 2, got %d", pins.Count(0x2000))
	}
	if err := exec.Complete(Receipt{Generation: gen, Slot: 0}); err != nil {
		t.Fatal(err)
	}
	if pins.Count(0x2000) != 1 {
		t.Fatal("page stays pinned while another descriptor is in flight")
	}
	if err := exec.Complete(Receipt{Generation: gen, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	if pins.Count(0x2000) != 0 {
		t.Fatal("page released only after the last completion")
	}
}

func TestExecutorResetReleasesInFlightAndInvalidatesReceipts(t *testing.T) {
	pins := NewPinSet()
	exec := NewExecutor(DeviceCallbackFunc(func(*FrozenChain) error { return nil }), pins)
	gen, err := exec.Submit(twoDescChain(t))
	if err != nil {
		t.Fatal(err)
	}

	exec.Reset()
	if exec.InFlight() != 0 {
		t.Fatal("reset must clear in-flight set")
	}
	if pins.Count(0x2000) != 0 || pins.Count(0x4000) != 0 {
		t.Fatal("reset must unpin all outstanding pages")
	}

	// 复位前未完成项的旧回执此后不能回收。
	if err := exec.Complete(Receipt{Generation: gen, Slot: 0}); !errors.Is(err, ErrStaleCompletion) {
		t.Fatalf("pre-reset receipt: want ErrStaleCompletion, got %v", err)
	}

	// 复位后代次继续前进，新提交不受影响。
	newGen, err := exec.Submit(twoDescChain(t))
	if err != nil {
		t.Fatal(err)
	}
	if newGen <= gen {
		t.Fatalf("generation must advance across reset: %d -> %d", gen, newGen)
	}
}

func TestExecutorGuestMutationAfterValidation(t *testing.T) {
	pins := NewPinSet()
	var submitted Descriptor
	exec := NewExecutor(DeviceCallbackFunc(func(chain *FrozenChain) error {
		submitted = chain.Descriptor(0)
		return nil
	}), pins)

	g := newFakeGuest(1)
	writeDesc(g, testRingBase, 0, Descriptor{Addr: 0x2000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd})
	chain, err := NewFreezer(nil, 0).Freeze(g, testRingBase, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	// 验证之后、提交之前改写客户机环。
	writeDesc(g, testRingBase, 0, Descriptor{Addr: 0x9000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd})
	if _, err := exec.Submit(chain); err != nil {
		t.Fatal(err)
	}
	if submitted.Addr != 0x2000 {
		t.Fatalf("callback must see frozen address, got %#x", submitted.Addr)
	}
}
