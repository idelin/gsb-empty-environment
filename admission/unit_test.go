package admission

import (
	"errors"
	"testing"
)

func TestRangeHelpers(t *testing.T) {
	if (Range{2, 5}).Len() != 4 {
		t.Fatal("Len")
	}
	if !(Range{2, 5}).Contains(3) || (Range{2, 5}).Contains(6) {
		t.Fatal("Contains")
	}
	if !(Range{0, 10}).Overlaps(Range{5, 20}) {
		t.Fatal("Overlaps true")
	}
	if (Range{0, 4}).Overlaps(Range{5, 20}) {
		t.Fatal("Overlaps false")
	}
	inter, ok := (Range{0, 10}).Intersect(Range{4, 20})
	if !ok || inter != (Range{4, 10}) {
		t.Fatalf("Intersect ok=%v inter=%v", ok, inter)
	}
	if _, ok := (Range{0, 1}).Intersect(Range{5, 6}); ok {
		t.Fatal("Intersect disjoint should fail")
	}
	merged := coalesceRanges([]Range{{0, 2}, {3, 5}, {9, 9}, {1, 4}})
	want := []Range{{0, 5}, {9, 9}}
	if len(merged) != len(want) {
		t.Fatalf("coalesce=%v want=%v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("coalesce=%v want=%v", merged, want)
		}
	}
}

func TestInvalidRangesAndShards(t *testing.T) {
	bad := []Range{
		{Start: 9, End: 1},           // 端点倒置
		{Start: 0, End: MaxPage + 1}, // 越界
		{Start: MaxPage, End: 0},     // 倒置
	}
	for _, r := range bad {
		if r.Valid() {
			t.Fatalf("range %v should be invalid", r)
		}
	}
	if validateShards(nil) {
		t.Fatal("nil shards invalid")
	}
	if validateShards([]Range{{0, 7}, {9, MaxPage}}) {
		t.Fatal("non-contiguous shards invalid")
	}
	if validateShards([]Range{{0, 7}, {8, MaxPage - 1}}) {
		t.Fatal("incomplete coverage invalid")
	}
	if !validateShards([]Range{{0, 7}, {8, MaxPage}}) {
		t.Fatal("valid partition rejected")
	}
}

func TestCheckpointValidation(t *testing.T) {
	cp := baseCheckpoint()
	if err := cp.Valid(); err != nil {
		t.Fatalf("valid checkpoint rejected: %v", err)
	}

	noRoot := cp
	noRoot.Root = nil
	if !errors.Is(noRoot.Valid(), ErrBadReceipt) {
		t.Fatal("missing root should fail")
	}

	overlap := cp
	overlap.Base = []BaseContent{
		{Pages: Range{0, 10}, Digest: digest("a"), Trust: TrustTrusted},
		{Pages: Range{5, 12}, Digest: digest("b"), Trust: TrustTrusted},
	}
	if !errors.Is(overlap.Valid(), ErrBadReceipt) {
		t.Fatal("overlapping base content should fail")
	}
}

// 多内容条目共享同一摘要（别名），条目精确拼接覆盖作用区间。
func TestMultiEntryContentReceipt(t *testing.T) {
	shared := digest("shared")
	rcpt := contentReceipt("vendor", "R0", "R1", Range{0, 11},
		ContentEntry{Pages: Range{0, 5}, Digest: shared},
		ContentEntry{Pages: Range{6, 11}, Digest: shared})
	if err := validateReceipt(rcpt, testShards()); err != nil {
		t.Fatalf("valid multi-entry receipt: %v", err)
	}

	// 条目未完全覆盖作用区间必须被拒绝。
	gap := contentReceipt("vendor", "R0", "R2", Range{0, 11},
		ContentEntry{Pages: Range{0, 4}, Digest: shared},
		ContentEntry{Pages: Range{6, 11}, Digest: shared})
	if !errors.Is(validateReceipt(gap, testShards()), ErrBadReceipt) {
		t.Fatal("content gap should be rejected")
	}
}

func TestPermissionReceiptValidation(t *testing.T) {
	// 权限收据带内容条目必须被拒绝。
	bad := Receipt{
		Publisher:  "vendor",
		OldRoot:    root("R0"),
		NewRoot:    root("R1"),
		Pages:      Range{0, 10},
		Kind:       KindPermission,
		RevokeExec: []Range{{0, 10}},
		Content:    []ContentEntry{{Pages: Range{0, 10}, Digest: digest("x")}},
	}
	if !errors.Is(validateReceipt(bad, testShards()), ErrBadReceipt) {
		t.Fatal("permission receipt with content should fail")
	}

	// 撤销区间超出作用页区间必须被拒绝。
	outOfScope := permReceipt("vendor", "R0", "R2", Range{0, 5}, Range{0, 9})
	if !errors.Is(validateReceipt(outOfScope, testShards()), ErrBadReceipt) {
		t.Fatal("out-of-scope revoke should fail")
	}
}

// 空请求区间非法；单页请求返回单页判定。
func TestSinglePageRequest(t *testing.T) {
	receipts := []Receipt{
		permReceipt("vendor", "R0", "R1", Range{0, 10}, Range{7, 7}),
	}
	in := baseInput(receipts, [][]byte{root("R1")})
	in.Request = Range{7, 7}
	got := runAdmit(t, in)
	want := map[Verdict][]Range{
		VerdictForbidden: {{7, 7}},
	}
	assertVerdicts(t, got, want)
}

func TestVerifierFunc(t *testing.T) {
	called := false
	vf := VerifierFunc(func(Receipt) Trust {
		called = true
		return TrustTrusted
	})
	if vf.VerifyReceipt(Receipt{}) != TrustTrusted || !called {
		t.Fatal("VerifierFunc wiring broken")
	}
}
