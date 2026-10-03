package bitmap

import (
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// ---------- 容器自动转换 ----------

func TestSparseIDsAutoArray(t *testing.T) {
	bm := New()
	for i := 0; i < 100; i++ {
		bm.Add(uint32(i * 500)) // 离散稀疏
	}
	s := bm.Stats()
	if s.Containers != 1 || s.Array != 1 {
		t.Fatalf("expect 1 array container, got %+v", s)
	}
	if bm.Cardinality() != 100 {
		t.Fatalf("cardinality = %d, want 100", bm.Cardinality())
	}
	for i := 0; i < 100; i++ {
		if !bm.Contains(uint32(i * 500)) {
			t.Fatalf("missing %d", i*500)
		}
	}
	if bm.Contains(1) {
		t.Fatal("unexpected hit")
	}
}

func TestDenseIDsAutoBitmap(t *testing.T) {
	bm := New()
	for i := 0; i < 5000; i++ {
		bm.Add(uint32(i)) // 超过 4096 阈值
	}
	s := bm.Stats()
	if s.Bitmap != 1 {
		t.Fatalf("expect bitmap container, got %+v", s)
	}
	if bm.Cardinality() != 5000 {
		t.Fatalf("cardinality = %d, want 5000", bm.Cardinality())
	}
}

func TestRangeAutoRun(t *testing.T) {
	bm := New()
	bm.AddRange(100, 200000) // 跨 3 个高 16 位键的连续区间
	s := bm.Stats()
	if s.Run == 0 {
		t.Fatalf("expect run containers, got %+v", s)
	}
	if bm.Cardinality() != 200000-100 {
		t.Fatalf("cardinality = %d, want %d", bm.Cardinality(), 200000-100)
	}
	for _, v := range []uint32{100, 65535, 65536, 131072, 199999} {
		if !bm.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	for _, v := range []uint32{0, 99, 200000} {
		if bm.Contains(v) {
			t.Fatalf("unexpected %d", v)
		}
	}
}

func TestArrayToBitmapTransition(t *testing.T) {
	bm := New()
	for i := 0; i < arrayToBitmapThreshold; i++ {
		bm.Add(uint32(i * 2))
	}
	if s := bm.Stats(); s.Array != 1 {
		t.Fatalf("at threshold expect array, got %+v", s)
	}
	bm.Add(uint32(arrayToBitmapThreshold * 2)) // 第 4097 个
	if s := bm.Stats(); s.Bitmap != 1 {
		t.Fatalf("above threshold expect bitmap, got %+v", s)
	}
}

func TestBitmapToArrayTransition(t *testing.T) {
	bm := New()
	for i := 0; i < 5000; i++ {
		bm.Add(uint32(i))
	}
	if s := bm.Stats(); s.Bitmap != 1 {
		t.Fatalf("expect bitmap, got %+v", s)
	}
	for i := 0; i < 5000-arrayToBitmapThreshold; i++ {
		bm.Remove(uint32(i))
	}
	if s := bm.Stats(); s.Array != 1 {
		t.Fatalf("expect downgrade to array, got %+v", s)
	}
	if bm.Cardinality() != arrayToBitmapThreshold {
		t.Fatalf("cardinality = %d", bm.Cardinality())
	}
}

func TestRunDegradesWhenFragmented(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100)
	if s := bm.Stats(); s.Run != 1 {
		t.Fatalf("expect run, got %+v", s)
	}
	// 把连续区间打成大量碎片后，RLE 不再紧凑，应自动降级为数组。
	for i := uint32(1); i < 100; i += 2 {
		bm.Remove(i)
	}
	if s := bm.Stats(); s.Run != 0 {
		t.Fatalf("expect run to degrade, got %+v", s)
	}
	if bm.Cardinality() != 50 {
		t.Fatalf("cardinality = %d, want 50", bm.Cardinality())
	}
}

func TestContainerEmptiedIsRemoved(t *testing.T) {
	bm := New()
	bm.Add(1 << 16)
	bm.Add(2 << 16)
	bm.Remove(1 << 16)
	bm.Remove(2 << 16)
	if !bm.IsEmpty() {
		t.Fatal("expect empty")
	}
	if s := bm.Stats(); s.Containers != 0 {
		t.Fatalf("expect 0 containers, got %+v", s)
	}
	if len(bm.tombs) != 2 {
		t.Fatalf("expect 2 tombstones, got %d", len(bm.tombs))
	}
}

// ---------- 集合运算正确性（对照参考实现） ----------

func refAnd(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		if b[v] {
			out[v] = true
		}
	}
	return out
}

func refOr(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		out[v] = true
	}
	for v := range b {
		out[v] = true
	}
	return out
}

func refXor(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for v := range a {
		if !b[v] {
			out[v] = true
		}
	}
	for v := range b {
		if !a[v] {
			out[v] = true
		}
	}
	return out
}

func toSet(bm *Bitmap) map[uint32]bool {
	out := map[uint32]bool{}
	for _, v := range bm.ToSlice() {
		out[v] = true
	}
	return out
}

func eqSet(t *testing.T, name string, got *Bitmap, want map[uint32]bool) {
	t.Helper()
	gs := toSet(got)
	if len(gs) != len(want) {
		t.Fatalf("%s: |got|=%d |want|=%d", name, len(gs), len(want))
	}
	for v := range want {
		if !gs[v] {
			t.Fatalf("%s: missing %d", name, v)
		}
	}
}

// 构造混合容器形态的位图：数组（稀疏）、位图（密集随机）、RLE（区间）。
func mixedBitmap(rnd *rand.Rand, base uint32) *Bitmap {
	bm := New()
	// 稀疏数组容器
	for i := 0; i < 200; i++ {
		bm.Add(base + uint32(rnd.Intn(60000)))
	}
	// 密集位图容器
	denseKey := base + 3<<16
	for i := 0; i < 6000; i++ {
		bm.Add(denseKey + uint32(rnd.Intn(65536)))
	}
	// RLE 容器
	bm.AddRange(uint64(base)+5<<16+10, uint64(base)+5<<16+40000)
	return bm
}

func TestOpsAgainstReference(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		a := mixedBitmap(rnd, uint32(trial)*7<<16)
		b := mixedBitmap(rnd, uint32(trial)*7<<16)
		sa, sb := toSet(a), toSet(b)
		eqSet(t, "and", And(a, b), refAnd(sa, sb))
		eqSet(t, "or", Or(a, b), refOr(sa, sb))
		eqSet(t, "xor", Xor(a, b), refXor(sa, sb))
	}
}

func TestBatchOps(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	bms := make([]*Bitmap, 5)
	sets := make([]map[uint32]bool, 5)
	for i := range bms {
		bms[i] = mixedBitmap(rnd, uint32(i)*3<<16)
		sets[i] = toSet(bms[i])
	}
	andAll := And(bms...)
	orAll := Or(bms...)
	xorAll := Xor(bms...)

	wantAnd := sets[0]
	wantOr := sets[0]
	wantXor := sets[0]
	for _, s := range sets[1:] {
		wantAnd = refAnd(wantAnd, s)
		wantOr = refOr(wantOr, s)
		wantXor = refXor(wantXor, s)
	}
	eqSet(t, "batch-and", andAll, wantAnd)
	eqSet(t, "batch-or", orAll, wantOr)
	eqSet(t, "batch-xor", xorAll, wantXor)
}

func TestOpsWithEmpty(t *testing.T) {
	e1, e2 := New(), New()
	if !And(e1, e2).IsEmpty() || !Or(e1, e2).IsEmpty() || !Xor(e1, e2).IsEmpty() {
		t.Fatal("ops on empty bitmaps must be empty")
	}
	b := New()
	b.Add(42)
	if !And(e1, b).IsEmpty() {
		t.Fatal("and with empty must be empty")
	}
	eqSet(t, "or-empty", Or(e1, b), map[uint32]bool{42: true})
	eqSet(t, "xor-empty", Xor(e1, b), map[uint32]bool{42: true})
	if !And().IsEmpty() || !Or().IsEmpty() {
		t.Fatal("nullary ops must be empty")
	}
}

func TestOpsDoNotMutateInputs(t *testing.T) {
	a, b := New(), New()
	a.AddRange(0, 100000)
	b.AddRange(50000, 150000)
	ca, cb := a.Cardinality(), b.Cardinality()
	_, _, _ = And(a, b), Or(a, b), Xor(a, b)
	if a.Cardinality() != ca || b.Cardinality() != cb {
		t.Fatal("ops mutated inputs")
	}
}

func TestFullDomainRange(t *testing.T) {
	bm := New()
	bm.AddRange(0, 1<<32) // 全部 2^32 个值
	if bm.Cardinality() != 1<<32 {
		t.Fatalf("cardinality = %d", bm.Cardinality())
	}
	if !bm.Contains(0) || !bm.Contains(0xFFFFFFFF) {
		t.Fatal("boundary values missing")
	}
	s := bm.Stats()
	if s.Run != 65536 {
		t.Fatalf("expect 65536 run containers, got %+v", s)
	}
}

func TestIteratorSorted(t *testing.T) {
	bm := New()
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		bm.Add(uint32(rnd.Intn(1 << 24)))
	}
	bm.AddRange(1<<25, 1<<25+5000)
	it := bm.Iterator()
	prev := uint32(0)
	first := true
	n := 0
	for v, ok := it.Next(); ok; v, ok = it.Next() {
		if !first && v <= prev {
			t.Fatalf("iterator not strictly increasing at %d", v)
		}
		first = false
		prev = v
		n++
	}
	if uint64(n) != bm.Cardinality() {
		t.Fatalf("iterated %d, cardinality %d", n, bm.Cardinality())
	}
}

// ---------- 并发安全 ----------

func TestConcurrentReadWrite(t *testing.T) {
	bm := New()
	const writers = 4
	const perWriter = 20000
	var wgWriters sync.WaitGroup
	for w := 0; w < writers; w++ {
		wgWriters.Add(1)
		go func(id int) {
			defer wgWriters.Done()
			base := uint32(id) * 1 << 20
			for i := 0; i < perWriter; i++ {
				bm.Add(base + uint32(i))
			}
		}(w)
	}

	stop := make(chan struct{})
	var wgReaders sync.WaitGroup
	for r := 0; r < 3; r++ {
		wgReaders.Add(1)
		go func() {
			defer wgReaders.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := bm.Snapshot()
				_ = s.Cardinality()
				it := s.Iterator()
				for i := 0; i < 100; i++ {
					if _, ok := it.Next(); !ok {
						break
					}
				}
				s.Release()
				_ = bm.Contains(12345)
				_ = And(bm, bm)
			}
		}()
	}
	wgWriters.Wait()
	close(stop)
	wgReaders.Wait()

	if got := bm.Cardinality(); got != writers*perWriter {
		t.Fatalf("cardinality = %d, want %d", got, writers*perWriter)
	}
	if len(bm.snaps) != 0 || bm.maxSnap != 0 {
		t.Fatalf("snapshot state leaked: snaps=%d maxSnap=%d", len(bm.snaps), bm.maxSnap)
	}
}

func TestConcurrentCheckpointer(t *testing.T) {
	dir := tempDir(t)
	bm := New()
	cp, err := NewCheckpointer(bm, dir)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint32(0); ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			bm.Add(i % (1 << 22))
			if i%5000 == 0 {
				bm.Remove(i % (1 << 22))
			}
		}
	}()
	// 归档期间并发写入不得 panic / race。
	for i := 0; i < 20; i++ {
		if _, err := cp.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if _, err := cp.Incremental(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	// 最终归档必须能完整恢复。
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Cardinality() != bm.Cardinality() {
		t.Fatalf("restored=%d live=%d", restored.Cardinality(), bm.Cardinality())
	}
	if d := Xor(restored, bm); d.Cardinality() != 0 {
		t.Fatalf("restored bitmap differs by %d elements", d.Cardinality())
	}
}

// 保证 sort 包被使用（占位，防止未来重构遗漏有序性假设）。
var _ = sort.Ints
