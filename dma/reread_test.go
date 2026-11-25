package dma

import (
	"errors"
	"testing"
)

const protectedAddr uint64 = 0xF000

// TestRereadAdapterReproducesOutOfBoundsDMA 重现 TOCTOU 攻击：
// 描述符在冻结阶段指向合法页，同步点在校验通过后把客户机环中的
// 地址改写到受保护页；脆弱适配器执行时重读内存，越界 DMA 被总线拦截。
func TestRereadAdapterReproducesOutOfBoundsDMA(t *testing.T) {
	g := newFakeGuest(1)
	writeDesc(g, testRingBase, 0, Descriptor{
		Addr: 0x2000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
	})

	pt := NewPageTable()
	if err := pt.Register(0x2000, PageSize, PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}

	chain, err := NewFreezer(pt, 0).Freeze(g, testRingBase, 1, 0)
	if err != nil {
		t.Fatalf("validation must pass before attack: %v", err)
	}

	bus := &faultBus{allowed: []region{{base: 0x2000, end: 0x3000, perm: PermRead | PermWrite}}}
	adapter := NewRereadAdapter(g, testRingBase, bus)
	adapter.SyncPoint = func(chain *FrozenChain) {
		// 攻击者在验证后、执行前改写客户机可写环（版本号同步推进）。
		writeDesc(g, testRingBase, 0, Descriptor{
			Addr: protectedAddr, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
		})
	}

	err = adapter.Submit(chain)
	if !errors.Is(err, errRereadOOB) {
		t.Fatalf("vulnerable adapter must reproduce OOB DMA, got %v", err)
	}
	if len(bus.hits) != 0 {
		t.Fatalf("faulting transfer must not be recorded, hits=%v", bus.hits)
	}

	// 私有冻结链始终保留校验时的合法地址，证明数据结构本身未被污染。
	if chain.Descriptor(0).Addr != 0x2000 {
		t.Fatalf("frozen chain changed: %#x", chain.Descriptor(0).Addr)
	}
}

// TestSafeExecutorIgnoresPostValidationRewrite 对照实验：同样的同步点
// 改写对正确实现完全无效，回调只看到冻结数组中的合法地址。
func TestSafeExecutorIgnoresPostValidationRewrite(t *testing.T) {
	g := newFakeGuest(1)
	writeDesc(g, testRingBase, 0, Descriptor{
		Addr: 0x2000, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
	})
	pt := NewPageTable()
	if err := pt.Register(0x2000, PageSize, PermRead|PermWrite); err != nil {
		t.Fatal(err)
	}
	chain, err := NewFreezer(pt, 0).Freeze(g, testRingBase, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	bus := &faultBus{allowed: []region{{base: 0x2000, end: 0x3000, perm: PermRead | PermWrite}}}
	var seen Descriptor
	cb := DeviceCallbackFunc(func(chain *FrozenChain) error {
		seen = chain.Descriptor(0)
		return bus.Transfer(seen.Addr, seen.Length, seen.Perm())
	})
	exec := NewExecutor(cb, NewPinSet())

	// 即使客户机环在提交瞬间被改写，提交数组也不受影响。
	writeDesc(g, testRingBase, 0, Descriptor{
		Addr: protectedAddr, Length: 64, Next: 0, Flags: FlagRead | FlagEnd,
	})
	if _, err := exec.Submit(chain); err != nil {
		t.Fatalf("safe path must not fault: %v", err)
	}
	if seen.Addr != 0x2000 {
		t.Fatalf("callback saw rewritten address %#x", seen.Addr)
	}
	if len(bus.hits) != 1 || bus.hits[0].Addr != 0x2000 {
		t.Fatalf("only the frozen legal transfer may execute, hits=%v", bus.hits)
	}
}
