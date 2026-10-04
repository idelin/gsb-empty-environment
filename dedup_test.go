package main

import "testing"

func TestWindowInOrder(t *testing.T) {
	w := NewSourceWindow(8)
	for seq := uint64(1); seq <= 5; seq++ {
		if _, err := w.Add(seq); err != nil {
			t.Fatalf("Add(%d): %v", seq, err)
		}
	}
	if w.Watermark() != 5 {
		t.Fatalf("watermark = %d, want 5", w.Watermark())
	}
	if w.GapLen() != 0 {
		t.Fatalf("gaps = %d, want 0", w.GapLen())
	}
}

func TestWindowOutOfOrderAbsorb(t *testing.T) {
	w := NewSourceWindow(8)
	for _, seq := range []uint64{1, 2, 4, 5, 7} {
		if _, err := w.Add(seq); err != nil {
			t.Fatalf("Add(%d): %v", seq, err)
		}
	}
	if w.Watermark() != 2 {
		t.Fatalf("watermark = %d, want 2", w.Watermark())
	}
	if got := w.Gaps(); len(got) != 3 || got[0] != 4 || got[1] != 5 || got[2] != 7 {
		t.Fatalf("gaps = %v, want [4 5 7]", got)
	}
	// 填补 3 后应吸收 4、5，水位前进到 5，缺口只剩 7。
	if _, err := w.Add(3); err != nil {
		t.Fatal(err)
	}
	if w.Watermark() != 5 {
		t.Fatalf("watermark = %d, want 5", w.Watermark())
	}
	if got := w.Gaps(); len(got) != 1 || got[0] != 7 {
		t.Fatalf("gaps = %v, want [7]", got)
	}
}

func TestWindowDuplicates(t *testing.T) {
	w := NewSourceWindow(8)
	for _, seq := range []uint64{1, 2, 4} {
		if _, err := w.Add(seq); err != nil {
			t.Fatal(err)
		}
	}
	for _, dup := range []uint64{1, 2, 4} {
		if !w.Seen(dup) {
			t.Fatalf("Seen(%d) = false, want true", dup)
		}
		if advanced, err := w.Add(dup); err != nil || advanced {
			t.Fatalf("Add(%d) = (%v, %v), want (false, nil)", dup, advanced, err)
		}
	}
	if w.Seen(3) || w.Seen(9) {
		t.Fatal("unseen seq reported as seen")
	}
}

func TestWindowGapBound(t *testing.T) {
	w := NewSourceWindow(2)
	if _, err := w.Add(1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add(3); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add(4); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Add(5); err != ErrGapTableFull {
		t.Fatalf("Add(5) err = %v, want ErrGapTableFull", err)
	}
	// 缺口表满不影响顺序到达。
	if _, err := w.Add(2); err != nil {
		t.Fatalf("Add(2): %v", err)
	}
	if w.Watermark() != 4 {
		t.Fatalf("watermark = %d, want 4", w.Watermark())
	}
}

func TestWindowCompress(t *testing.T) {
	w := NewSourceWindow(8)
	for _, seq := range []uint64{1, 2, 3, 4, 6} {
		if _, err := w.Add(seq); err != nil {
			t.Fatal(err)
		}
	}
	// 压缩水位不得超过连续水位：否则离线补传时未收到的旧消息
	// 会被误判为重复而丢失。
	if err := w.Compress(5); err == nil {
		t.Fatal("Compress(5) with watermark 4 should fail")
	}
	if err := w.Compress(4); err != nil {
		t.Fatal(err)
	}
	if w.Floor() != 4 {
		t.Fatalf("floor = %d, want 4", w.Floor())
	}
	if !w.Seen(3) {
		t.Fatal("seq below floor should be seen")
	}
	if got := w.Gaps(); len(got) != 1 || got[0] != 6 {
		t.Fatalf("gaps = %v, want [6]", got)
	}
	// 压缩水位只升不降。
	if err := w.Compress(2); err != nil {
		t.Fatal(err)
	}
	if w.Floor() != 4 {
		t.Fatalf("floor regressed to %d", w.Floor())
	}
}
