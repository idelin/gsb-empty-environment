package handover

import (
	"errors"
	"testing"
)

func TestInputValidation(t *testing.T) {
	tab := &Table{Handles: []Handle{{Slot: 1, Gen: 1, Object: "a", Perm: PermRead}}}
	role := Role{Name: "r", Target: 9, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()}

	if _, err := BuildPlan(nil, []Role{role}, 99); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil table: %v", err)
	}
	if _, err := BuildPlan(tab, nil, 99); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no roles: %v", err)
	}
	if _, err := BuildPlan(tab, []Role{{Name: "", Target: 9, Allowed: Allow("a"), Share: ShareWith()}}, 99); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := BuildPlan(tab, []Role{role, {Name: "r2", Target: 9, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()}}, 99); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("dup target: %v", err)
	}
	occupiedTemp := &Table{Handles: []Handle{{Slot: 99, Gen: 1, Object: "t", Perm: PermRead}}}
	if _, err := BuildPlan(occupiedTemp, []Role{role}, 99); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("occupied temp: %v", err)
	}
}

func TestSharingAllowedReusesObject(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "a", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 10, Allowed: Allow("a"), Need: PermRead, Share: ShareWith("r2")},
		{Name: "r2", Target: 11, Allowed: Allow("a"), Need: PermRead, Share: ShareWith("r1")},
	}
	p := assertPlan(t, tab, roles, 99, []int{1, 2}, 1)
	if len(p.UsedObjects) != 1 || p.UsedObjects[0] != "a" {
		t.Fatalf("shared object reuse failed: %v", p.UsedObjects)
	}

	// 单向允许不构成共享，必须失败。
	roles[1].Share = ShareWith()
	if _, err := BuildPlan(tab, roles, 99); !errors.Is(err, ErrNoAssignment) {
		t.Fatalf("one-way share must fail, got %v", err)
	}
}

func TestVerifyEmptySlotAndObjectMismatch(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 7, Object: "a", Perm: PermRead},
	}}
	roles := []Role{{Name: "r", Target: 10, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()}}
	p, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatal(err)
	}

	// 期望为空的目标槽被占用。
	blocked := NewMemTable(&Table{Handles: []Handle{
		{Slot: 1, Gen: 7, Object: "a", Perm: PermRead},
		{Slot: 10, Gen: 1, Object: "x", Perm: PermRead},
	}})
	if err := p.VerifyExpectations(blocked); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("occupied expected-empty slot: %v", err)
	}

	// 期望占用的源槽变空。
	missing := NewMemTable(&Table{})
	if err := p.VerifyExpectations(missing); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("missing source: %v", err)
	}
}

func TestGenerationRewindRejected(t *testing.T) {
	tab := &Table{Handles: []Handle{{Slot: 1, Gen: 100, Object: "a", Perm: PermRead}}}
	roles := []Role{{Name: "r", Target: 10, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()}}
	p, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatal(err)
	}

	rewound := NewMemTable(&Table{Handles: []Handle{
		{Slot: 1, Gen: 99, Object: "a", Perm: PermRead}, // 代次回绕
	}})
	if err := p.VerifyExpectations(rewound); !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("rewound generation must fail verification, got %v", err)
	}
}

func TestValidatorRejectsIllegalOps(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 10, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
		{Name: "r2", Target: 11, Allowed: Allow("b"), Need: PermRead, Share: ShareWith()},
	}
	base, err := BuildPlan(tab, roles, 99)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]Op{
		"move onto occupied":  {{Kind: OpMove, A: 1, B: 2}},
		"swap with empty":     {{Kind: OpSwap, A: 1, B: 7}},
		"overlapping parswap": {{Kind: OpParSwap, Pairs: [][2]int{{1, 2}, {2, 3}}}},
		"store without load":  {{Kind: OpStore, A: 99, B: 10}},
		"barrier with temp":   {{Kind: OpLoad, A: 99, B: 1}, {Kind: OpBarrier}},
		"setcloexec empty":    {{Kind: OpSetCloexec, A: 7, On: true}},
	}
	for name, ops := range cases {
		bad := *base
		bad.Ops = ops
		if err := bad.Validate(tab); err == nil {
			t.Fatalf("validator accepted %s", name)
		}
	}
}

func TestExecutorErrorsAndClose(t *testing.T) {
	mt := NewMemTable(&Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
	}})
	if err := mt.Swap(1, 2); err == nil {
		t.Fatal("swap with empty must fail")
	}
	if err := mt.Move(2, 3); err == nil {
		t.Fatal("move empty source must fail")
	}
	if err := mt.Move(1, 1); err == nil {
		t.Fatal("move into occupied must fail")
	}
	if err := mt.SetCloexec(2, true); err == nil {
		t.Fatal("setcloexec empty must fail")
	}
	if err := mt.Close(1); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := mt.Close(1); err == nil {
		t.Fatal("double close must fail")
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 4, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 7, Gen: 1, Object: "b", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 30, Allowed: Allow("a", "b"), Need: PermRead, Share: ShareWith()},
		{Name: "r2", Target: 31, Allowed: Allow("a"), Need: PermRead, Share: ShareWith()},
	}
	// 共享被禁止，r1 不能取对象 a；唯一可行首槽是 7（对象 b），r2 再取槽1。
	assertPlan(t, tab, roles, 99, []int{7, 1}, 2)
}

func TestLexicographicAmongEqualObjectCounts(t *testing.T) {
	tab := &Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "c", Perm: PermRead},
		{Slot: 5, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 6, Gen: 1, Object: "c", Perm: PermRead},
	}}
	roles := []Role{
		{Name: "r1", Target: 30, Allowed: Allow("a", "c"), Need: PermRead, Share: ShareWith()},
		{Name: "r2", Target: 31, Allowed: Allow("a", "c"), Need: PermRead, Share: ShareWith()},
	}
	// 任意两个不同槽都是 2 对象解；字典序最小为 [1 2]。
	assertPlan(t, tab, roles, 99, []int{1, 2}, 2)
}

func TestPermHelpers(t *testing.T) {
	if !(PermRead).SubsetOf(PermRead | PermWrite) {
		t.Fatal("read should subset rw")
	}
	if (PermRead | PermWrite).SubsetOf(PermRead) {
		t.Fatal("rw must not subset read")
	}
	if !PermRead.Readable() || PermRead.Writable() || !(PermRead | PermWrite).Writable() {
		t.Fatal("perm helpers wrong")
	}
	if PermRead.String() != "r" || (PermRead|PermWrite).String() != "rw" {
		t.Fatal("perm string wrong")
	}
}

func TestParSwapEmptyAndUnknownOp(t *testing.T) {
	mt := NewMemTable(&Table{Handles: []Handle{
		{Slot: 1, Gen: 1, Object: "a", Perm: PermRead},
		{Slot: 2, Gen: 1, Object: "b", Perm: PermRead},
	}})
	if err := mt.ParSwap([][2]int{{1, 3}}); err == nil {
		t.Fatal("parswap with empty slot must fail")
	}
	if err := applyOp(mt, Op{Kind: OpKind(99)}); err == nil {
		t.Fatal("unknown op must fail")
	}
	if got := (OpKind(99)).String(); got == "" {
		t.Fatal("opkind string empty")
	}
	if got := (Op{Kind: OpClose, A: 1}).String(); got != "close(1)" {
		t.Fatalf("op string = %q", got)
	}
}

func TestCommitAppliesCloexecAndCloseOps(t *testing.T) {
	tab := &Table{Handles: []Handle{{Slot: 1, Gen: 1, Object: "a", Perm: PermRead, Cloexec: false}}}
	mt := NewMemTable(tab)
	if err := applyOp(mt, Op{Kind: OpSetCloexec, A: 1, On: true}); err != nil {
		t.Fatal(err)
	}
	if h, _ := mt.HandleAt(1); !h.Cloexec {
		t.Fatal("cloexec not applied")
	}
	if err := applyOp(mt, Op{Kind: OpClose, A: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mt.HandleAt(1); ok {
		t.Fatal("close did not remove handle")
	}
	if err := applyOp(mt, Op{Kind: OpBarrier}); err != nil {
		t.Fatal(err)
	}
	if err := applyOp(mt, Op{Kind: OpDropTemp, A: 99}); err != nil {
		t.Fatal(err)
	}
}
