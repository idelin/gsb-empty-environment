package offlinecli

import (
	"errors"
	"testing"
)

// TestTaintChain 构造经过 COPY 原始区、ADD、COPY 已生成区（含重叠）的输出，
// 验证受保护输出区的首个不可信字节及完整回溯链。
func TestTaintChain(t *testing.T) {
	// 原镜像 8 字节，仅 [0,4) 可信；[4,8) 不可信。
	c, err := NewChecker(8, []Range{{Start: 0, End: 4}})
	if err != nil {
		t.Fatal(err)
	}
	ops := []Op{
		{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 8},                      // out[0:8] <- orig[0:8]
		{Type: OpAdd, Dst: 8, Len: 4, Data: []byte{0xAA, 0xBB, 0xCC, 0xDD}}, // out[8:12] ADD
		// 重叠复制：out[12:16] <- 已生成的 out[4:8]（不可信原区）。
		{Type: OpCopyGenerated, Src: 4, Dst: 12, Len: 4},
		// 重叠复制：out[16:20] <- 已生成的 out[8:12]（ADD 数据）。
		{Type: OpCopyGenerated, Src: 8, Dst: 16, Len: 4},
		// 可信再复制：out[20:24] <- out[0:4] <- orig[0:4]。
		{Type: OpCopyGenerated, Src: 0, Dst: 20, Len: 4},
	}
	if err := c.ApplyPatch(ops); err != nil {
		t.Fatal(err)
	}
	if got := c.OutputSize(); got != 24 {
		t.Fatalf("output size = %d, want 24", got)
	}

	// 受保护区覆盖整个输出，首个不可信字节应是 out[4]（不可信原区）。
	finding, err := c.Check([]Range{{Start: 0, End: 24}})
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil {
		t.Fatal("expected a taint finding")
	}
	if finding.Offset != 4 {
		t.Fatalf("first tainted offset = %d, want 4", finding.Offset)
	}

	// 只检查可信区域 [0,4) 与 [20,24)，应全部通过。
	clean, err := c.Check([]Range{{Start: 20, End: 24}, {Start: 0, End: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if clean != nil {
		t.Fatalf("expected no finding, got %+v", clean)
	}

	// 检查经一次 generated 复制落到不可信原区的链：out[12] -> out[4] -> orig[4]。
	f, err := c.Check([]Range{{Start: 12, End: 13}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Offset != 12 || len(f.Hops) != 2 {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if f.Hops[0].OpIndex != 2 || f.Hops[0].SrcKind != srcGenerated || f.Hops[0].Offset != 12 {
		t.Fatalf("hop0 mismatch: %+v", f.Hops[0])
	}
	if f.Hops[1].SrcKind != srcOriginal || f.Hops[1].Source != 4 {
		t.Fatalf("hop1 mismatch: %+v", f.Hops[1])
	}

	// 经 generated 复制落到 ADD 数据的链：out[16] -> out[8] -> ADD[0]。
	f, err = c.Check([]Range{{Start: 16, End: 17}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || len(f.Hops) != 2 || f.Hops[1].SrcKind != srcAdd || f.Hops[1].Source != 0 {
		t.Fatalf("unexpected ADD chain: %+v", f)
	}
}

// TestPartialTrustBoundary 验证可信区间边界内外的逐字节判定与区间合并。
func TestPartialTrustBoundary(t *testing.T) {
	c, err := NewChecker(10, []Range{{Start: 2, End: 5}, {Start: 5, End: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 10}); err != nil {
		t.Fatal(err)
	}
	// 可信原区合并后为 [2,7)，因此受保护的 out[0:10] 首个污点在 out[0]。
	f, err := c.Check([]Range{{Start: 0, End: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Offset != 0 {
		t.Fatalf("unexpected finding: %+v", f)
	}
	// 仅保护映射到可信原区的输出 [2,7)，应通过。
	f, err = c.Check([]Range{{Start: 2, End: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Fatalf("expected clean range, got %+v", f)
	}
}

// TestSegmentSplitting 验证覆盖写会正确切分旧段，保留未覆盖部分的来源。
func TestSegmentSplitting(t *testing.T) {
	c, err := NewChecker(8, []Range{{Start: 0, End: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 8}); err != nil {
		t.Fatal(err)
	}
	// ADD 只能追加；先在尾部 out[8:10] 放入新增数据，再用 COPY 已生成区覆盖 out[3:5]。
	if err := c.Apply(Op{Type: OpAdd, Dst: 8, Len: 2, Data: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(Op{Type: OpCopyGenerated, Src: 8, Dst: 3, Len: 2}); err != nil {
		t.Fatal(err)
	}
	f, err := c.Check([]Range{{Start: 0, End: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Offset != 3 {
		t.Fatalf("expected first taint at 3, got %+v", f)
	}
	// 紧邻边界的 out[2]、out[5] 仍来自可信原区。
	f, err = c.Check([]Range{{Start: 2, End: 3}})
	if err != nil || f != nil {
		t.Fatalf("out[2] should be clean, f=%+v err=%v", f, err)
	}
	f, err = c.Check([]Range{{Start: 5, End: 6}})
	if err != nil || f != nil {
		t.Fatalf("out[5] should be clean, f=%+v err=%v", f, err)
	}

	// 直接检查尾部新增数据 out[8:10]，应命中 ADD 叶子。
	f, err = c.Check([]Range{{Start: 8, End: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Offset != 8 || len(f.Hops) != 1 || f.Hops[0].SrcKind != srcAdd {
		t.Fatalf("expected ADD finding at 8, got %+v", f)
	}
}

// TestDeepChain 验证多跳 generated 复制的完整回溯链。
func TestDeepChain(t *testing.T) {
	c, err := NewChecker(4, []Range{{Start: 0, End: 2}})
	if err != nil {
		t.Fatal(err)
	}
	ops := []Op{
		{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4},  // out[0:4]，orig[2:4] 不可信
		{Type: OpCopyGenerated, Src: 2, Dst: 4, Len: 2}, // out[4:6] <- out[2:4]
		{Type: OpCopyGenerated, Src: 4, Dst: 6, Len: 2}, // out[6:8] <- out[4:6]
	}
	if err := c.ApplyPatch(ops); err != nil {
		t.Fatal(err)
	}
	f, err := c.Check([]Range{{Start: 6, End: 8}})
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || f.Offset != 6 {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if len(f.Hops) != 3 {
		t.Fatalf("chain depth = %d, want 3: %+v", len(f.Hops), f.Hops)
	}
	wantIdx := []int{2, 1, 0}
	for i, want := range wantIdx {
		if f.Hops[i].OpIndex != want {
			t.Fatalf("hop %d op index = %d, want %d", i, f.Hops[i].OpIndex, want)
		}
	}
	leaf := f.Hops[2]
	if leaf.SrcKind != srcOriginal || leaf.Source != 2 {
		t.Fatalf("leaf mismatch: %+v", leaf)
	}
}

// TestProtectedRangeOutOfBounds 验证保护区间越界报错。
func TestProtectedRangeOutOfBounds(t *testing.T) {
	c, _ := NewChecker(4, nil)
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Check([]Range{{Start: 0, End: 5}})
	if !errors.Is(err, ErrProtectedRangeOutOfBounds) {
		t.Fatalf("got %v", err)
	}
}

// TestTrustedRangeClamped 验证超出原镜像的可信区间会被裁剪而非误判。
func TestTrustedRangeClamped(t *testing.T) {
	c, err := NewChecker(4, []Range{{Start: 0, End: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4}); err != nil {
		t.Fatal(err)
	}
	f, err := c.Check([]Range{{Start: 0, End: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Fatalf("all original bytes should be trusted after clamp, got %+v", f)
	}
}
