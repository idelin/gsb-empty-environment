package slotpool

import "testing"

// 回收复用后旧句柄必须失效。
func TestStaleHandleInvalidatedAfterReuse(t *testing.T) {
	p := New(1, 4)

	h1, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := p.Release(h1); err != nil {
		t.Fatalf("Release: %v", err)
	}

	h2, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	if h2.Slot != h1.Slot {
		t.Fatalf("expected slot %d reused, got %d", h1.Slot, h2.Slot)
	}
	if h2.Gen != h1.Gen+1 {
		t.Fatalf("expected gen %d, got %d", h1.Gen+1, h2.Gen)
	}
	if p.Alive(h1) {
		t.Fatal("stale handle still alive")
	}
	if err := p.Release(h1); err != ErrInvalidHandle {
		t.Fatalf("Release(stale) = %v, want ErrInvalidHandle", err)
	}
	if err := p.Delete(h1); err != ErrInvalidHandle {
		t.Fatalf("Delete(stale) = %v, want ErrInvalidHandle", err)
	}
	if err := p.Release(h2); err != nil {
		t.Fatalf("Release(current) = %v", err)
	}
}

// 借用期间删除只标记待回收，最后释放才复用。
func TestDeleteDuringBorrowDefersReclaim(t *testing.T) {
	p := New(1, 4)

	h, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := p.Delete(h); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// 已标记待回收，但槽仍被借用，不得复用。
	if _, err := p.Acquire(); err != ErrPoolExhausted {
		t.Fatalf("Acquire while pending delete = %v, want ErrPoolExhausted", err)
	}
	// 重复删除幂等。
	if err := p.Delete(h); err != nil {
		t.Fatalf("Delete again: %v", err)
	}
	// 句柄在释放前仍然有效。
	if !p.Alive(h) {
		t.Fatal("handle should stay alive until release")
	}
	// 最后释放触发回收，槽以新代号复用。
	if err := p.Release(h); err != nil {
		t.Fatalf("Release: %v", err)
	}
	h2, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if h2.Slot != h.Slot || h2.Gen != h.Gen+1 {
		t.Fatalf("got %+v, want slot %d gen %d", h2, h.Slot, h.Gen+1)
	}
	if p.Alive(h) {
		t.Fatal("old handle should be invalid after recycle")
	}
}

// 代号溢出时槽永久退休，不能绕回。
func TestGenerationOverflowRetiresSlot(t *testing.T) {
	const genBits = 2 // 代号空间 {1,2,3}
	p := New(1, genBits)

	var gens []uint32
	for {
		h, err := p.Acquire()
		if err == ErrPoolExhausted {
			break
		}
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		gens = append(gens, h.Gen)
		if err := p.Delete(h); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := p.Release(h); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}

	want := []uint32{1, 2, 3}
	if len(gens) != len(want) {
		t.Fatalf("used gens %v, want %v", gens, want)
	}
	for i := range want {
		if gens[i] != want[i] {
			t.Fatalf("gen wrapped or skipped: got %v, want %v", gens, want)
		}
	}
	// 退休是永久的。
	for i := 0; i < 10; i++ {
		if _, err := p.Acquire(); err != ErrPoolExhausted {
			t.Fatalf("Acquire after retire = %v, want ErrPoolExhausted", err)
		}
	}
	free, borrowed, retired := p.Stats()
	if free != 0 || borrowed != 0 || retired != 1 {
		t.Fatalf("Stats = (%d,%d,%d), want (0,0,1)", free, borrowed, retired)
	}
}

// 穷举：小代号空间下遍历所有创建/借用/删除/释放的操作交错序列，
// 验证不变量。容量 2、代号空间 {1}（每槽仅可用一次即退休）。
func TestExhaustiveInterleavings(t *testing.T) {
	const (
		capacity = 2
		genBits  = 1
		maxDepth = 8
	)

	type op struct {
		kind  int // 0=Acquire 1=Release 2=Delete
		which int // 指向已发放句柄的下标（kind != 0 时有效）
	}

	var seq []op

	// replay 在新建的池上重放 seq，逐步校验不变量。
	replay := func() {
		p := New(capacity, genBits)
		issued := make([]Handle, 0, capacity)
		seen := make(map[Handle]bool)
		live := make([]bool, 0, capacity)

		checkStats := func(step int) {
			f, b, r := p.Stats()
			if f+b+r != capacity {
				t.Fatalf("seq %v step %d: Stats=(%d,%d,%d) sum != %d", seq, step, f, b, r, capacity)
			}
		}

		for step, o := range seq {
			switch o.kind {
			case 0: // Acquire
				h, err := p.Acquire()
				if err != nil {
					if err != ErrPoolExhausted {
						t.Fatalf("seq %v step %d: Acquire = %v", seq, step, err)
					}
					// 耗尽必须是一致的：再试仍然耗尽。
					if _, err2 := p.Acquire(); err2 != ErrPoolExhausted {
						t.Fatalf("seq %v step %d: exhausted pool revived", seq, step)
					}
					break
				}
				if h.Gen == 0 {
					t.Fatalf("seq %v step %d: zero-gen handle %+v", seq, step, h)
				}
				if seen[h] {
					t.Fatalf("seq %v step %d: handle %+v issued twice (gen wrapped)", seq, step, h)
				}
				seen[h] = true
				issued = append(issued, h)
				live = append(live, true)
			case 1, 2: // Release / Delete
				h := issued[o.which]
				var err error
				if o.kind == 1 {
					err = p.Release(h)
				} else {
					err = p.Delete(h)
				}
				if live[o.which] {
					if err != nil {
						t.Fatalf("seq %v step %d: op on live handle %+v = %v", seq, step, h, err)
					}
					if o.kind == 1 {
						live[o.which] = false
					}
				} else {
					// 已释放的句柄必须永久失效。
					if err != ErrInvalidHandle {
						t.Fatalf("seq %v step %d: stale handle %+v accepted", seq, step, h)
					}
				}
			}
			checkStats(step)
		}
	}

	var walk func(depth int)
	walk = func(depth int) {
		replay()
		if depth == maxDepth {
			return
		}
		// 统计当前已发放句柄数（上限 capacity*(1<<genBits-1)）。
		maxHandles := capacity * (1<<uint(genBits) - 1)
		// 候选操作：Acquire，以及对每个已发放句柄（无论死活）Release/Delete。
		// 已发放句柄数由序列中 Acquire 成功次数决定，重放才能得到；
		// 这里用保守上界，越界下标在重放时跳过。
		count := 0
		for _, o := range seq {
			if o.kind == 0 {
				count++
			}
		}
		if count > maxHandles {
			count = maxHandles
		}
		seq = append(seq, op{kind: 0})
		walk(depth + 1)
		seq = seq[:len(seq)-1]
		for i := 0; i < count; i++ {
			for _, kind := range []int{1, 2} {
				seq = append(seq, op{kind: kind, which: i})
				walk(depth + 1)
				seq = seq[:len(seq)-1]
			}
		}
	}
	walk(0)
}

// 非法句柄：零值、越界槽号、错误代号。
func TestInvalidHandles(t *testing.T) {
	p := New(2, 2)
	got, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	idle := got.Slot ^ 1 // 另一个槽未借出
	bogus := []Handle{
		{},
		{Slot: 0, Gen: 0},
		{Slot: 0, Gen: 99},
		{Slot: idle, Gen: 1},
		{Slot: 7, Gen: 1},
	}
	for _, h := range bogus {
		if err := p.Release(h); err != ErrInvalidHandle {
			t.Errorf("Release(%+v) = %v, want ErrInvalidHandle", h, err)
		}
		if err := p.Delete(h); err != ErrInvalidHandle {
			t.Errorf("Delete(%+v) = %v, want ErrInvalidHandle", h, err)
		}
		if p.Alive(h) {
			t.Errorf("Alive(%+v) = true, want false", h)
		}
	}
}
