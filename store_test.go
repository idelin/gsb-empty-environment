package rootslot

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// reopen 在崩溃后的模拟器上重新打开存储。
func reopen(t *testing.T, sim *SimBackend, v Validator) *Store {
	t.Helper()
	s, err := OpenBackend(sim, v)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	return s
}

func mustCommit(t *testing.T, s *Store, root []byte) {
	t.Helper()
	if err := s.Commit(root); err != nil {
		t.Fatalf("commit %q failed: %v", root, err)
	}
}

func TestEmptyRepoOpens(t *testing.T) {
	s := reopen(t, NewSimBackend(), nil)
	if s.Seq() != 0 || s.Root() != nil {
		t.Fatalf("empty repo: got seq=%d root=%q", s.Seq(), s.Root())
	}
}

func TestCommitSurvivesPowerLoss(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("root-A"))
	sim.Crash(true)
	s2 := reopen(t, sim, nil)
	if string(s2.Root()) != "root-A" || s2.Seq() != 1 {
		t.Fatalf("after power loss: seq=%d root=%q", s2.Seq(), s2.Root())
	}
}

func TestConsecutiveCommitsAlternateSlots(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	roots := []string{"r1", "r2", "r3", "r4", "r5"}
	for i, r := range roots {
		mustCommit(t, s, []byte(r))
		sim.Crash(true) // 每次提交后都断电
		s = reopen(t, sim, nil)
		if string(s.Root()) != r || s.Seq() != uint64(i+1) {
			t.Fatalf("commit %d: seq=%d root=%q", i+1, s.Seq(), s.Root())
		}
	}
}

// 对新槽写入的每一个字节边界做撕裂注入：恢复结果只能是旧根（完整写除外）。
func TestTornWriteEveryBoundary(t *testing.T) {
	for keep := 0; keep <= SlotSize; keep++ {
		t.Run(fmt.Sprintf("keep=%d", keep), func(t *testing.T) {
			sim := NewSimBackend()
			sim.TornAt = map[int]int{}
			s := reopen(t, sim, nil)
			mustCommit(t, s, []byte("old-root"))
			sim.TornAt[1] = keep // 第二次提交的槽写被撕裂
			mustCommit(t, s, []byte("new-root"))
			sim.Crash(true)
			s2 := reopen(t, sim, nil)
			want := "old-root"
			if keep == SlotSize {
				want = "new-root" // 完整写不是撕裂
			}
			if string(s2.Root()) != want {
				t.Fatalf("keep=%d: recovered %q, want %q", keep, s2.Root(), want)
			}
		})
	}
}

func TestDroppedSlotWrite(t *testing.T) {
	sim := NewSimBackend()
	sim.DropAt = map[int]bool{1: true}
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("old-root"))
	mustCommit(t, s, []byte("new-root")) // 写丢失但调用方看到成功
	sim.Crash(true)
	s2 := reopen(t, sim, nil)
	if string(s2.Root()) != "old-root" {
		t.Fatalf("recovered %q, want old-root", s2.Root())
	}
}

// 重排：槽写被推迟到 Sync 之后才落盘。断电丢新根，进程崩溃看得到新根。
func TestReorderedWrite(t *testing.T) {
	newSim := func() *SimBackend {
		sim := NewSimBackend()
		sim.DelayAt = map[int]bool{1: true}
		s := reopen(t, sim, nil)
		mustCommit(t, s, []byte("old-root"))
		mustCommit(t, s, []byte("new-root"))
		return sim
	}
	sim := newSim()
	sim.Crash(true) // 断电：重排的写未同步，丢失
	if got := string(reopen(t, sim, nil).Root()); got != "old-root" {
		t.Fatalf("power loss: recovered %q, want old-root", got)
	}
	sim = newSim()
	sim.Crash(false) // 进程崩溃：OS 页缓存仍在
	if got := string(reopen(t, sim, nil).Root()); got != "new-root" {
		t.Fatalf("process crash: recovered %q, want new-root", got)
	}
}

func TestLostSync(t *testing.T) {
	sim := NewSimBackend()
	sim.SyncNoopAt = map[int]bool{1: true}
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("old-root"))
	mustCommit(t, s, []byte("new-root"))
	sim.Crash(true)
	if got := string(reopen(t, sim, nil).Root()); got != "old-root" {
		t.Fatalf("recovered %q, want old-root", got)
	}
}

// 新槽半写（只落盘前半部分）时仍读旧根，且损坏记入诊断。
func TestHalfWrittenNewSlot(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("old-root"))
	half := encodeSlot(2, []byte("new-root"))[:SlotSize/2]
	sim.WriteAt(half, SlotSize) // 模拟崩溃在槽写中间
	sim.Sync()
	sim.Crash(true)
	s2 := reopen(t, sim, nil)
	if string(s2.Root()) != "old-root" {
		t.Fatalf("recovered %q, want old-root", s2.Root())
	}
	if len(s2.Diagnostics()) == 0 {
		t.Fatal("expected corruption diagnostic for half-written slot")
	}
}

func TestBothSlotsCorruptRefusesOpen(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("r1"))
	mustCommit(t, s, []byte("r2"))
	garbage := bytes.Repeat([]byte{0xAB}, SlotSize)
	sim.WriteAt(garbage, 0)
	sim.WriteAt(garbage, SlotSize)
	sim.Sync()
	sim.Crash(true)
	if _, err := OpenBackend(sim, nil); !errors.Is(err, ErrBothSlotsCorrupt) {
		t.Fatalf("want ErrBothSlotsCorrupt, got %v", err)
	}
}

// 相同序号、不同内容：必须诊断冲突，拒绝打开。
func TestSameSeqDifferentContentConflict(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("r1")) // slot0 seq=1
	sim.WriteAt(encodeSlot(1, []byte("forged")), SlotSize)
	sim.Sync()
	sim.Crash(true)
	if _, err := OpenBackend(sim, nil); !errors.Is(err, ErrSlotConflict) {
		t.Fatalf("want ErrSlotConflict, got %v", err)
	}
}

// 相同序号、相同内容：幂等发布，合法。
func TestSameSeqSameContentOK(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("r1"))
	sim.WriteAt(encodeSlot(1, []byte("r1")), SlotSize)
	sim.Sync()
	sim.Crash(true)
	s2 := reopen(t, sim, nil)
	if string(s2.Root()) != "r1" {
		t.Fatalf("recovered %q, want r1", s2.Root())
	}
}

// 旧槽重放：目标槽已持有 >= 本次序号的有效记录时拒绝提交。
func TestReplayDetection(t *testing.T) {
	sim := NewSimBackend()
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("r1")) // slot0 seq1
	mustCommit(t, s, []byte("r2")) // slot1 seq2
	// 外部把 slot0 改写成 seq=7 的有效记录（重放/伪造）。
	sim.WriteAt(encodeSlot(7, []byte("replayed")), 0)
	sim.Sync()
	err := s.Commit([]byte("r3")) // 内存中 seq=2，目标 slot0 已是 seq7
	if !errors.Is(err, ErrReplayDetected) {
		t.Fatalf("want ErrReplayDetected, got %v", err)
	}
}

// 序号环绕策略：耗尽即拒绝，绝不回绕。
func TestSequenceExhaustedNoWrap(t *testing.T) {
	sim := NewSimBackend()
	sim.WriteAt(encodeSlot(maxSeq, []byte("last")), 0)
	sim.Sync()
	sim.Crash(true)
	s := reopen(t, sim, nil)
	if s.Seq() != maxSeq {
		t.Fatalf("seq=%d, want maxSeq", s.Seq())
	}
	if err := s.Commit([]byte("overflow")); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("want ErrSequenceExhausted, got %v", err)
	}
}

// 验证器拒绝时槽绝不发布。
func TestValidatorBlocksPublish(t *testing.T) {
	sim := NewSimBackend()
	v := ValidatorFunc(func(root []byte) error {
		if bytes.Equal(root, []byte("bad")) {
			return errors.New("unreachable closure")
		}
		return nil
	})
	s := reopen(t, sim, v)
	mustCommit(t, s, []byte("good"))
	writesBefore := sim.WriteCount
	if err := s.Commit([]byte("bad")); err == nil {
		t.Fatal("commit should fail validation")
	}
	if sim.WriteCount != writesBefore {
		t.Fatal("slot must not be written when validation fails")
	}
	sim.Crash(true)
	if got := string(reopen(t, sim, v).Root()); got != "good" {
		t.Fatalf("recovered %q, want good", got)
	}
}

// 可达闭包验证：根页及可达页完整可读才允许发布。
func TestPageClosureValidation(t *testing.T) {
	pages := MemPages{
		1: InternalPage(2, 3),
		2: LeafPage(),
		3: InternalPage(4),
		4: LeafPage(),
	}
	v := &PageValidator{Pages: pages}
	sim := NewSimBackend()
	s := reopen(t, sim, v)
	mustCommit(t, s, RootDescriptor(1))
	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	delete(pages, 4) // 可达页丢失
	if err := s.Commit(RootDescriptor(1)); err == nil {
		t.Fatal("commit with unreadable reachable page must fail")
	}
	if err := s.Verify(); err == nil {
		t.Fatal("independent verify must fail for broken closure")
	}
}

// 序号空洞（中间提交丢失）应记入诊断但不拒绝打开。
func TestSequenceGapDiagnostic(t *testing.T) {
	sim := NewSimBackend()
	sim.WriteAt(encodeSlot(1, []byte("r1")), 0)
	sim.WriteAt(encodeSlot(5, []byte("r5")), SlotSize)
	sim.Sync()
	sim.Crash(true)
	s := reopen(t, sim, nil)
	if string(s.Root()) != "r5" {
		t.Fatalf("root=%q, want r5", s.Root())
	}
	if len(s.Diagnostics()) == 0 {
		t.Fatal("expected sequence-gap diagnostic")
	}
}

// 枚举：N 次连续提交，在第 K 次提交注入各类故障后断电，
// 恢复结果只能是“旧根或新根”——具体为最后一个完整同步提交的根。
func TestFaultEnumerationAcrossCommits(t *testing.T) {
	type fault struct {
		name  string
		apply func(sim *SimBackend, writeIdx, syncIdx int)
		// 故障是否阻止该次提交持久化
		prevents bool
	}
	faults := []fault{
		{"none", func(*SimBackend, int, int) {}, false},
		{"torn", func(s *SimBackend, w, _ int) { s.TornAt = map[int]int{w: 100} }, true},
		{"drop", func(s *SimBackend, w, _ int) { s.DropAt = map[int]bool{w: true} }, true},
		{"reorder", func(s *SimBackend, w, _ int) { s.DelayAt = map[int]bool{w: true} }, true},
		{"sync-lost", func(s *SimBackend, _, y int) { s.SyncNoopAt = map[int]bool{y: true} }, true},
	}
	for n := 1; n <= 5; n++ {
		for _, f := range faults {
			for k := 1; k <= n; k++ {
				name := fmt.Sprintf("n=%d/%s/k=%d", n, f.name, k)
				t.Run(name, func(t *testing.T) {
					sim := NewSimBackend()
					s := reopen(t, sim, nil)
					roots := make([][]byte, 0, n)
					for c := 1; c <= n; c++ {
						if c == k {
							f.apply(sim, c-1, c-1)
						}
						root := []byte(fmt.Sprintf("root-%d", c))
						mustCommit(t, s, root)
						roots = append(roots, root)
					}
					sim.Crash(true)
					s2 := reopen(t, sim, nil)
					// 期望：若故障阻止了第 k 次提交且 k 是最后一次，
					// 则恢复到第 k-1 次；否则恢复到最后一次。
					wantIdx := n - 1
					if f.prevents && k == n {
						wantIdx = n - 2
					}
					var want []byte
					if wantIdx >= 0 {
						want = roots[wantIdx]
					}
					if !bytes.Equal(s2.Root(), want) {
						t.Fatalf("recovered %q, want %q", s2.Root(), want)
					}
					if s2.Seq() != uint64(wantIdx+1) {
						t.Fatalf("seq=%d, want %d", s2.Seq(), wantIdx+1)
					}
				})
			}
		}
	}
}

// 进程崩溃与断电的保证边界：未同步的写在进程崩溃后可见，断电后丢失。
func TestProcessCrashVsPowerLossBoundary(t *testing.T) {
	sim := NewSimBackend()
	sim.SyncNoopAt = map[int]bool{1: true} // 第二次同步丢失
	s := reopen(t, sim, nil)
	mustCommit(t, s, []byte("durable-root"))
	mustCommit(t, s, []byte("volatile-only-root"))

	sim.Crash(false) // 进程崩溃：已写字节仍在
	if got := string(reopen(t, sim, nil).Root()); got != "volatile-only-root" {
		t.Fatalf("process crash: got %q", got)
	}

	sim.Crash(true) // 断电：只剩已同步字节
	if got := string(reopen(t, sim, nil).Root()); got != "durable-root" {
		t.Fatalf("power loss: got %q", got)
	}
}
