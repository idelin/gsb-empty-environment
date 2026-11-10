package offlinecli

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

// TestZeroLengthOps 验证零长操作是无操作：
// 即使源是尚未生成的前向位置也不报错，且不改变输出大小。
func TestZeroLengthOps(t *testing.T) {
	c, err := NewChecker(8, []Range{{Start: 0, End: 4}})
	if err != nil {
		t.Fatal(err)
	}

	zeroOps := []Op{
		{Type: OpCopyOriginal, Src: 100, Dst: 0, Len: 0},      // 源越界但零长
		{Type: OpCopyGenerated, Src: 1 << 40, Dst: 0, Len: 0}, // 前向引用但零长
		{Type: OpAdd, Dst: 0, Len: 0},
		{Type: OpCopyGenerated, Src: math.MaxUint64, Dst: math.MaxUint64, Len: 0},
	}
	for i, op := range zeroOps {
		if err := c.Apply(op); err != nil {
			t.Fatalf("zero-length op %d should be a no-op, got %v", i, err)
		}
	}
	if got := c.OutputSize(); got != 0 {
		t.Fatalf("output size should stay 0 after zero-length ops, got %d", got)
	}
	if len(c.segments) != 0 {
		t.Fatalf("no segments should be recorded, got %d", len(c.segments))
	}

	// ADD 声明零长却携带数据属于畸形补丁，不能静默丢弃数据。
	err = c.Apply(Op{Type: OpAdd, Len: 0, Data: []byte{1}})
	if !errors.Is(err, ErrMalformedPatch) {
		t.Fatalf("expected ErrMalformedPatch for zero-len add with data, got %v", err)
	}
}

// TestIllegalForwardReference 验证 COPY 已生成区只能引用已经写出的字节。
func TestIllegalForwardReference(t *testing.T) {
	tests := []struct {
		name string
		ops  []Op
		bad  int
	}{
		{
			name: "src at current tail",
			ops: []Op{
				{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4},
				{Type: OpCopyGenerated, Src: 4, Dst: 4, Len: 1},
			},
			bad: 1,
		},
		{
			name: "src beyond current output",
			ops: []Op{
				{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4},
				{Type: OpCopyGenerated, Src: 8, Dst: 0, Len: 2},
			},
			bad: 1,
		},
		{
			name: "span crosses tail",
			ops: []Op{
				{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 4},
				{Type: OpCopyGenerated, Src: 3, Dst: 8, Len: 4},
			},
			bad: 1,
		},
		{
			name: "reference into empty output",
			ops:  []Op{{Type: OpCopyGenerated, Src: 0, Dst: 0, Len: 1}},
			bad:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewChecker(16, []Range{{Start: 0, End: 16}})
			if err != nil {
				t.Fatal(err)
			}
			for i, op := range tt.ops {
				err := c.Apply(op)
				if i == tt.bad {
					if !errors.Is(err, ErrIllegalForwardReference) {
						t.Fatalf("op %d: expected ErrIllegalForwardReference, got %v", i, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("op %d unexpected error: %v", i, err)
				}
			}
			t.Fatal("expected an illegal forward reference error")
		})
	}
}

// TestLegalForwardOverlap 验证允许的向前重叠复制（memmove 语义）。
func TestLegalForwardOverlap(t *testing.T) {
	c, err := NewChecker(8, []Range{{Start: 0, End: 8}})
	if err != nil {
		t.Fatal(err)
	}
	ops := []Op{
		{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 8}, // out[0:8] <- orig[0:8]
		// 源 [2,6) 全部已写出，目标 [2,6) 与源重叠，但不引用任何未生成位置。
		{Type: OpCopyGenerated, Src: 2, Dst: 2, Len: 4},
	}
	if err := c.ApplyPatch(ops); err != nil {
		t.Fatalf("overlapping generated copy should be legal: %v", err)
	}
	if got := c.OutputSize(); got != 8 {
		t.Fatalf("output size = %d, want 8", got)
	}
	// out[5] 由 op1 映射到父输出 2+(5-2)=5，再由 op0 映射到原镜像偏移 5-(5-3)=3。
	hops, err := c.ResolveChain(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 || hops[0].OpIndex != 1 || hops[1].SrcKind != srcOriginal || hops[1].Source != 3 {
		t.Fatalf("unexpected chain: %+v", hops)
	}
}

// TestRepeatedOverlappingFill 在固定缓冲区内模拟 memmove 式逐字节前推：
// 历史快照必须始终指向每次复制前的旧来源，回溯不能成环。
func TestRepeatedOverlappingFill(t *testing.T) {
	c, err := NewChecker(6, []Range{{Start: 0, End: 6}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 6}); err != nil {
		t.Fatal(err)
	}
	// 连续 4 次 src=dst-1 的重叠复制，dst 从 1 推进到 4，输出大小不变。
	for i := 0; i < 4; i++ {
		dst := uint64(i + 1)
		if err := c.Apply(Op{Type: OpCopyGenerated, Src: dst - 1, Dst: dst, Len: 2}); err != nil {
			t.Fatalf("fill step %d: %v", i, err)
		}
	}
	if got := c.OutputSize(); got != 6 {
		t.Fatalf("output size = %d, want 6", got)
	}
	// out[5] 经历多轮重叠覆盖，必须能有限步回溯到原镜像字节，而不是在段间死循环。
	hops, err := c.ResolveChain(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 5 {
		t.Fatalf("chain depth = %d, want 5: %+v", len(hops), hops)
	}
	if last := hops[len(hops)-1]; last.SrcKind != srcOriginal || last.Source != 0 {
		t.Fatalf("leaf should be original byte 0, got %+v", last)
	}
}

// TestLengthOverflow 验证所有长度算术都以 ErrLengthOverflow 拒绝。
func TestLengthOverflow(t *testing.T) {
	max := uint64(math.MaxUint64)

	t.Run("copy original src overflow", func(t *testing.T) {
		c, _ := NewChecker(8, nil)
		err := c.Apply(Op{Type: OpCopyOriginal, Src: max, Dst: 0, Len: 1})
		if !errors.Is(err, ErrLengthOverflow) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("generated copy src overflow", func(t *testing.T) {
		c, _ := NewChecker(8, nil)
		err := c.Apply(Op{Type: OpCopyGenerated, Src: max - 1, Dst: 0, Len: 5})
		if !errors.Is(err, ErrLengthOverflow) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("dst overflow", func(t *testing.T) {
		c, _ := NewChecker(8, nil)
		err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: max, Len: 2})
		if !errors.Is(err, ErrLengthOverflow) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("output growth overflow", func(t *testing.T) {
		c, _ := NewChecker(8, nil)
		// 先把输出推进到接近 uint64 上界是不现实的；改为用 ADD len 触发
		// dst(=outSize)+len 与 outSize+len 的溢出路径。
		err := c.Apply(Op{Type: OpAdd, Dst: 0, Len: max, Data: nil})
		if !errors.Is(err, ErrLengthOverflow) && !errors.Is(err, ErrMalformedPatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("add length mismatches data", func(t *testing.T) {
		c, _ := NewChecker(8, nil)
		err := c.Apply(Op{Type: OpAdd, Dst: 0, Len: max, Data: make([]byte, 1)})
		if !errors.Is(err, ErrMalformedPatch) {
			t.Fatalf("got %v", err)
		}
	})
}

// TestOriginalOutOfBounds 区分溢出与普通越界。
func TestOriginalOutOfBounds(t *testing.T) {
	c, _ := NewChecker(8, nil)
	err := c.Apply(Op{Type: OpCopyOriginal, Src: 6, Dst: 0, Len: 4})
	if !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("expected ErrOutOfBounds, got %v", err)
	}
}

// TestGapWriteRejected 验证不允许跳过空洞写出。
func TestGapWriteRejected(t *testing.T) {
	c, _ := NewChecker(8, nil)
	if err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 0, Len: 2}); err != nil {
		t.Fatal(err)
	}
	err := c.Apply(Op{Type: OpCopyOriginal, Src: 0, Dst: 5, Len: 1})
	if !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("expected ErrOutOfBounds for gap write, got %v", err)
	}
}

// TestEncodeDecodeRoundTrip 验证补丁编解码自洽。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	ops := []Op{
		{Type: OpCopyOriginal, Src: 1, Dst: 0, Len: 2},
		{Type: OpAdd, Dst: 2, Len: 3, Data: []byte{9, 8, 7}}, // 应用时 Dst 须等于当前尾部
		{Type: OpCopyGenerated, Src: 0, Dst: 5, Len: 4},
		{Type: OpAdd, Len: 0},
	}
	raw, err := EncodePatch(ops)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePatch(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ops) {
		t.Fatalf("decoded %d ops, want %d", len(got), len(ops))
	}
	for i := range ops {
		if got[i].Type != ops[i].Type || got[i].Src != ops[i].Src ||
			got[i].Dst != ops[i].Dst || got[i].Len != ops[i].Len ||
			!bytes.Equal(got[i].Data, ops[i].Data) {
			t.Fatalf("op %d mismatch: got %+v want %+v", i, got[i], ops[i])
		}
	}
}
