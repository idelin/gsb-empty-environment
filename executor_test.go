package netsec

import (
	"errors"
	"testing"
)

// recordingSubmit 记录提交时冻结链的内容。
type recordingSubmit struct {
	chains [][]Descriptor
	err    error
	hook   func()
}

func (r *recordingSubmit) submit(chain *FrozenChain) error {
	if r.err != nil {
		return r.err
	}
	if r.hook != nil {
		r.hook()
	}
	cp := make([]Descriptor, chain.Len())
	for i := range cp {
		cp[i] = chain.At(i)
	}
	r.chains = append(r.chains, cp)
	return nil
}

func twoDescRing(mem *fakeMemory) {
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Next: 1, Flags: FlagRead},
		1: {Addr: 0x400, Len: 0x80, Flags: FlagWrite | FlagEnd},
	})
}

func TestSubmitAndCompleteLifecycle(t *testing.T) {
	mem, _, fz := newTestRig()
	twoDescRing(mem)
	rec := &recordingSubmit{}
	ex := NewExecutor(fz, rec.submit)
	gen, err := ex.Submit(testRingBase, 0)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if gen != 1 {
		t.Fatalf("gen = %d, want 1", gen)
	}
	if n := ex.InFlight(); n != 2 {
		t.Fatalf("InFlight = %d, want 2", n)
	}
	if err := ex.Complete(gen, 0); err != nil {
		t.Fatalf("Complete(0): %v", err)
	}
	expectErr(t, ex.Complete(gen, 0), ErrDuplicateCompletion)
	if n := ex.InFlight(); n != 1 {
		t.Fatalf("InFlight after dup = %d, want 1", n)
	}
	expectErr(t, ex.Complete(gen+100, 0), ErrStaleGeneration)
	expectErr(t, ex.Complete(gen, 2), ErrIndexOutOfRange)
	if err := ex.Complete(gen, 1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if n := ex.InFlight(); n != 0 {
		t.Fatalf("InFlight = %d, want 0", n)
	}
	expectErr(t, ex.Complete(gen, 0), ErrStaleGeneration)
}

// 设备回调失败：仅本次提交作废，此前在途页不得释放。
func TestSubmitFailureKeepsInflight(t *testing.T) {
	mem, _, fz := newTestRig()
	twoDescRing(mem)
	rec := &recordingSubmit{}
	ex := NewExecutor(fz, rec.submit)
	gen1, err := ex.Submit(testRingBase, 0)
	if err != nil {
		t.Fatalf("Submit #1: %v", err)
	}
	rec.err = errors.New("device busy")
	if _, err := ex.Submit(testRingBase, 0); err == nil {
		t.Fatal("Submit #2 should fail")
	}
	if n := ex.InFlight(); n != 2 {
		t.Fatalf("InFlight after failed submit = %d, want 2", n)
	}
	if err := ex.Complete(gen1, 0); err != nil {
		t.Fatalf("Complete gen1: %v", err)
	}
	if err := ex.Complete(gen1, 1); err != nil {
		t.Fatalf("Complete gen1: %v", err)
	}
	if n := ex.InFlight(); n != 0 {
		t.Fatalf("InFlight = %d, want 0", n)
	}
}

// 复位前未完成项：复位作废旧代次，之后的完成回执一律拒绝。
func TestResetWithOutstanding(t *testing.T) {
	mem, _, fz := newTestRig()
	twoDescRing(mem)
	rec := &recordingSubmit{}
	ex := NewExecutor(fz, rec.submit)
	gen, err := ex.Submit(testRingBase, 0)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := ex.Complete(gen, 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := ex.Reset(); got != 1 {
		t.Fatalf("Reset outstanding = %d, want 1", got)
	}
	if n := ex.InFlight(); n != 0 {
		t.Fatalf("InFlight after reset = %d, want 0", n)
	}
	expectErr(t, ex.Complete(gen, 1), ErrStaleGeneration)
	gen2, err := ex.Submit(testRingBase, 0)
	if err != nil {
		t.Fatalf("Submit after reset: %v", err)
	}
	if gen2 == gen {
		t.Fatalf("generation must advance across reset, got %d", gen2)
	}
	if err := ex.Complete(gen2, 0); err != nil {
		t.Fatalf("Complete gen2: %v", err)
	}
}

// 验证后改写：提交回调触发时客户机改写原环，冻结链内容不得改变。
func TestFrozenChainImmuneToPostValidationRewrite(t *testing.T) {
	mem, _, fz := newTestRig()
	twoDescRing(mem)
	rec := &recordingSubmit{}
	rec.hook = func() {
		mem.writeDesc(testRingBase, 0, Descriptor{
			Addr: 0x2000, Len: 0x40, Next: 1, Flags: FlagRead,
		})
	}
	ex := NewExecutor(fz, rec.submit)
	if _, err := ex.Submit(testRingBase, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got := rec.chains[0]
	if got[0].Addr != 0x100 {
		t.Fatalf("frozen desc 0 addr = %#x, want 0x100", got[0].Addr)
	}
}

// 待审的执行时重读适配器：重现验证后换地址造成的越界 DMA。
func TestRereadAdapterReproducesOOBDMA(t *testing.T) {
	mem, reg, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagWrite | FlagEnd},
	})
	adapter := &RereadAdapter{Mem: mem, RingBase: testRingBase, Head: 0}
	adapter.AfterValidate = func() {
		mem.writeDesc(testRingBase, 0, Descriptor{
			Addr: 0x2000, Len: 0x40, Flags: FlagRead | FlagWrite | FlagEnd,
		})
	}
	var dmaHits []uint64
	adapter.DMA = func(addr uint64, length uint32, flags uint16) error {
		dmaHits = append(dmaHits, addr)
		return nil
	}
	ex := NewExecutor(fz, adapter.Submit)
	if _, err := ex.Submit(testRingBase, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(dmaHits) != 1 {
		t.Fatalf("dma hits = %v", dmaHits)
	}
	if dmaHits[0] != 0x2000 {
		t.Fatalf("expected exploit to hit reserved page 0x2000, got %#x", dmaHits[0])
	}
	if err := reg.check(dmaHits[0], 0x40, PermRead|PermWrite); !errors.Is(err, ErrReserved) {
		t.Fatalf("sanity: hit address should be reserved, check err = %v", err)
	}
}

// 对照组：正确实现只使用冻结数组，同样的改写无法造成越界。
func TestFrozenExecutorBlocksRewrite(t *testing.T) {
	mem, _, fz := newTestRig()
	writeRing(mem, map[uint16]Descriptor{
		0: {Addr: 0x100, Len: 0x40, Flags: FlagRead | FlagWrite | FlagEnd},
	})
	var dmaHits []uint64
	recording := SubmitFunc(func(chain *FrozenChain) error {
		mem.writeDesc(testRingBase, 0, Descriptor{
			Addr: 0x2000, Len: 0x40, Flags: FlagRead | FlagWrite | FlagEnd,
		})
		for i := 0; i < chain.Len(); i++ {
			dmaHits = append(dmaHits, chain.At(i).Addr)
		}
		return nil
	})
	ex := NewExecutor(fz, recording)
	if _, err := ex.Submit(testRingBase, 0); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(dmaHits) != 1 || dmaHits[0] != 0x100 {
		t.Fatalf("frozen chain must keep validated addr 0x100, got %v", dmaHits)
	}
}
