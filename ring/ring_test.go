package ring

import "testing"

func TestSlidingWindow(t *testing.T) {
	r := New[int](3, 0)
	r.Put(0, 100)
	r.Put(1, 101)
	r.Put(2, 102)

	if !r.Contains(0) || !r.Contains(2) || r.Contains(3) {
		t.Fatalf("contains wrong before slide: oldest=%d", r.Oldest())
	}

	// 写入第 3 帧，窗口滑动，第 0 帧被淘汰。
	r.Put(3, 103)
	if r.Oldest() != 1 || r.Contains(0) || !r.Contains(3) {
		t.Fatalf("window did not slide: oldest=%d contains0=%v contains3=%v",
			r.Oldest(), r.Contains(0), r.Contains(3))
	}
	if got := r.Get(3); got != 103 {
		t.Fatalf("get(3)=%d want 103", got)
	}

	// 大幅跳跃后旧窗口全部失效。
	r.Put(10, 110)
	if r.Oldest() != 8 || r.Contains(7) || !r.Contains(10) {
		t.Fatalf("jump slide wrong: oldest=%d", r.Oldest())
	}
	if got := r.Get(10); got != 110 {
		t.Fatalf("get(10)=%d want 110", got)
	}
}

func TestRewriteAfterSlide(t *testing.T) {
	r := New[int](5, 0)
	for f := 0; f <= 10; f++ {
		r.Put(f, f*10)
	}
	// 窗口原点应已滑到 6，槽位保存帧 6..10。
	if r.Oldest() != 6 {
		t.Fatalf("oldest=%d want 6", r.Oldest())
	}
	// 重写窗口内的旧帧 7，不应移动原点或污染其他帧。
	r.Put(7, 777)
	if r.Oldest() != 6 {
		t.Fatalf("rewrite moved origin to %d", r.Oldest())
	}
	for f := 6; f <= 10; f++ {
		want := f * 10
		if f == 7 {
			want = 777
		}
		if got := r.Get(f); got != want {
			t.Errorf("frame %d: got %d want %d", f, got, want)
		}
	}
}

func TestNoAliasingAcrossCapacity(t *testing.T) {
	r := New[int](3, 0)
	r.Put(0, 0)
	r.Put(1, 1)
	r.Put(2, 2)
	r.Put(3, 3) // 原点滑到 1，帧 0 淘汰
	// 帧 3 与已淘汰的帧 0 共享物理槽，但帧 1,2,3 必须互不干扰。
	if r.Get(1) != 1 || r.Get(2) != 2 || r.Get(3) != 3 {
		t.Fatalf("aliasing: %d %d %d", r.Get(1), r.Get(2), r.Get(3))
	}
}
