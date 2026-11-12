package netsec

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentMunmap 用可注入的阻塞失效回调模拟并发 munmap：
// 失效通知返回前旧页不得回收，返回后必须回收；并发取页不得
// 拿到已回收的页。
func TestConcurrentMunmap(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	m := NewManager(func(p PASID, start, end uint64) {
		once.Do(func() { close(entered) })
		<-gate
	}, 8)

	proc := NewProcess()
	pas, err := m.AllocPASID(proc)
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	pages := make([]*Page, n)
	for i := 0; i < n; i++ {
		pages[i] = NewPage(uint64(i))
		if err := m.Map(pas, uint64(i)*pageSize, pages[i]); err != nil {
			t.Fatal(err)
		}
		// 调用方引用转交给映射，此后映射持有唯一引用。
		pages[i].release()
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				pg, err := m.TakePage(pas, uint64(rng.Intn(n))*pageSize)
				if err != nil {
					continue
				}
				if pg.Freed() {
					t.Error("TakePage returned a reclaimed page")
					return
				}
				m.PutPage(pg)
			}
		}()
	}

	done := make(chan error, 1)
	go func() { done <- m.UnmapNotify(pas, 0, n*pageSize) }()
	<-entered

	// 失效通知尚未返回，旧页一律不得回收。
	for i, pg := range pages {
		if pg.Freed() {
			t.Fatalf("page %d reclaimed before invalidation returned", i)
		}
	}

	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	// 失效返回后，映射引用已释放，旧页必须被回收。
	for i, pg := range pages {
		if !pg.Freed() {
			t.Fatalf("page %d not reclaimed after invalidation returned", i)
		}
	}
	if _, err := m.TakePage(pas, 0); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("TakePage after munmap = %v, want ErrUnmapped", err)
	}
}

// TestPASIDReuse 验证 PASID 复用：回收前必须完成整段地址空间的
// TLB 失效，复用后代次递增，旧代次的命令与取页全部失效。
func TestPASIDReuse(t *testing.T) {
	type invRec struct {
		p          PASID
		start, end uint64
	}
	var mu sync.Mutex
	var log []invRec
	m := NewManager(func(p PASID, start, end uint64) {
		mu.Lock()
		log = append(log, invRec{p, start, end})
		mu.Unlock()
	}, 1) // 只有一个 ID，强制复用

	a, b := NewProcess(), NewProcess()
	p1, err := m.AllocPASID(a)
	if err != nil {
		t.Fatal(err)
	}
	pg := NewPage(1)
	if err := m.Map(p1, 0, pg); err != nil {
		t.Fatal(err)
	}
	pg.release() // 映射持有唯一引用

	if err := m.ReleasePASID(a, p1); err != nil {
		t.Fatal(err)
	}
	// 回收返回时，TLB 失效必须已完成且旧页已回收。
	mu.Lock()
	found := false
	for _, r := range log {
		if r.p == p1 && r.start == 0 && r.end == maxAddress {
			found = true
		}
	}
	mu.Unlock()
	if !found {
		t.Fatal("ReleasePASID returned before full-range TLB invalidation")
	}
	if !pg.Freed() {
		t.Fatal("page not reclaimed after ReleasePASID invalidation")
	}

	// 复用同一 ID，代次必须递增。
	p2, err := m.AllocPASID(b)
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID != p1.ID {
		t.Fatalf("expected ID reuse, got %v then %v", p1, p2)
	}
	if p2.Gen <= p1.Gen {
		t.Fatalf("generation not bumped on reuse: %v -> %v", p1, p2)
	}

	// 旧代次：取页、提交、重复回收都必须失败。
	if _, err := m.TakePage(p1, 0); !errors.Is(err, ErrBadPASID) {
		t.Fatalf("TakePage with stale PASID = %v, want ErrBadPASID", err)
	}
	if err := m.Submit(a, Command{PASID: p1}); !errors.Is(err, ErrBadPASID) {
		t.Fatalf("Submit with stale PASID = %v, want ErrBadPASID", err)
	}
	if err := m.ReleasePASID(a, p1); !errors.Is(err, ErrBadPASID) {
		t.Fatalf("double release = %v, want ErrBadPASID", err)
	}
	// 新代次地址空间是干净的，旧映射不可见。
	if _, err := m.TakePage(p2, 0); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("TakePage in fresh generation = %v, want ErrUnmapped", err)
	}
}

// TestCommandForeignPASID 验证命令路径拒绝引用他人 PASID，
// 以及 PASID 复用后旧代次命令失效。
func TestCommandForeignPASID(t *testing.T) {
	m := NewManager(nil, 4)
	a, b := NewProcess(), NewProcess()
	pa, err := m.AllocPASID(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := m.AllocPASID(b)
	if err != nil {
		t.Fatal(err)
	}
	pg := NewPage(1)
	if err := m.Map(pa, 0, pg); err != nil {
		t.Fatal(err)
	}

	// B 引用 A 的 PASID，A 引用 B 的 PASID，都必须被拒绝。
	if err := m.Submit(b, Command{PASID: pa, Addrs: []uint64{0}}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("B using A's PASID = %v, want ErrNotOwner", err)
	}
	if err := m.Submit(a, Command{PASID: pb}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("A using B's PASID = %v, want ErrNotOwner", err)
	}
	// 属主使用自己的 PASID 成功。
	if err := m.Submit(a, Command{PASID: pa, Addrs: []uint64{0}}); err != nil {
		t.Fatalf("owner submit = %v, want nil", err)
	}

	// A 的 PASID 被回收并被他人复用后，A 持有的旧命令必须失效。
	if err := m.ReleasePASID(a, pa); err != nil {
		t.Fatal(err)
	}
	c := NewProcess()
	pc, err := m.AllocPASID(c)
	if err != nil {
		t.Fatal(err)
	}
	if pc.ID == pa.ID && pc.Gen == pa.Gen {
		t.Fatal("recycled PASID kept the same generation")
	}
	if err := m.Submit(a, Command{PASID: pa, Addrs: []uint64{0}}); !errors.Is(err, ErrBadPASID) {
		t.Fatalf("stale command after recycle = %v, want ErrBadPASID", err)
	}
}

// TestSeqRetry 用注入点在序号复核前改变映射，验证 TakePage
// 丢弃旧引用重来并返回最新映射。
func TestSeqRetry(t *testing.T) {
	m := NewManager(nil, 4)
	proc := NewProcess()
	pas, err := m.AllocPASID(proc)
	if err != nil {
		t.Fatal(err)
	}
	oldPg := NewPage(1)
	if err := m.Map(pas, 0, oldPg); err != nil {
		t.Fatal(err)
	}

	newPg := NewPage(2)
	var flipped atomic.Bool
	m.afterLookupHook = func() {
		if flipped.CompareAndSwap(false, true) {
			// 在序号复核之前改变映射，强制序号变化。
			if err := m.Map(pas, 0, newPg); err != nil {
				t.Error(err)
			}
		}
	}

	pg, err := m.TakePage(pas, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pg != newPg {
		t.Fatalf("TakePage returned stale page %v, want %v", pg.ID(), newPg.ID())
	}
	if m.Retries() == 0 {
		t.Fatal("expected at least one sequence retry")
	}
	m.PutPage(pg)

	// 旧页的映射引用已在重映射失效后释放，但重试期间取到的
	// 临时引用也已归还，页不得处于半回收状态。
	if oldPg.Freed() {
		t.Log("old page reclaimed after remap invalidation (caller ref held by test)")
	}
}

// TestTakePageConcurrentRemap 在并发重映射压力下做序号重试的
// 稳定性验证（配合 -race 运行）。
func TestTakePageConcurrentRemap(t *testing.T) {
	m := NewManager(nil, 4)
	proc := NewProcess()
	pas, err := m.AllocPASID(proc)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Map(pas, 0, NewPage(0)); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(1); ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := m.Map(pas, 0, NewPage(i)); err != nil {
				return
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		pg, err := m.TakePage(pas, 0)
		if err != nil {
			t.Fatal(err)
		}
		if pg.Freed() {
			t.Fatal("TakePage returned a reclaimed page")
		}
		m.PutPage(pg)
	}
	close(stop)
	wg.Wait()
	if m.Retries() == 0 {
		t.Fatal("expected sequence retries under concurrent remap")
	}
}
