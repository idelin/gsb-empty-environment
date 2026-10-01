package sparse

import (
	"math/rand"
	"testing"
	"time"
)

// ---------- 稠密参考模型（与实现语义逐条对应） ----------

type ref struct {
	a      []int64 // 实际值
	shadow []int64 // 若未固定应有的值（def + 惰性层增量）
	pinned []bool  // 是否例外（显式固定）
	def    int64
}

func newRef(n int, def int64) *ref {
	r := &ref{a: make([]int64, n), shadow: make([]int64, n), pinned: make([]bool, n), def: def}
	for i := range r.a {
		r.a[i], r.shadow[i] = def, def
	}
	return r
}

func (r *ref) clone() *ref {
	c := &ref{a: append([]int64(nil), r.a...), shadow: append([]int64(nil), r.shadow...),
		pinned: append([]bool(nil), r.pinned...), def: r.def}
	return c
}

func (r *ref) assign(p int, v int64) { r.a[p], r.pinned[p] = v, true }

func (r *ref) rangeAdd(l, rr int, d int64) {
	for i := l; i < rr; i++ {
		r.a[i] += d
		r.shadow[i] += d
	}
}

func (r *ref) changeDefault(d2 int64) {
	delta := d2 - r.def
	for i := range r.shadow {
		r.shadow[i] += delta
	}
	for i := range r.a {
		if !r.pinned[i] {
			r.a[i] = r.shadow[i] // 未固定位置跟随
		}
	}
	for i := range r.a {
		if r.pinned[i] && r.a[i] == r.shadow[i] {
			r.pinned[i] = false // 回收：值等于回收后读取值
		}
	}
	r.def = d2
}

func (r *ref) rangeSum(l, rr int) int64 {
	s := int64(0)
	for i := l; i < rr; i++ {
		s += r.a[i]
	}
	return s
}

func (r *ref) nonNeg() bool {
	for _, v := range r.a {
		if v < 0 {
			return false
		}
	}
	return true
}

func (r *ref) locate(cum int64) (uint64, bool) {
	if cum < 1 {
		return 0, false
	}
	prefix := int64(0)
	for i, v := range r.a {
		prefix += v
		if prefix >= cum {
			return uint64(i), true
		}
	}
	return 0, false
}

// ---------- 随机小数组参考验证 ----------

func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	type snapPair struct {
		s *Snapshot
		m *ref
	}
	for trial := 0; trial < 40; trial++ {
		n := 1 + rng.Intn(200)
		def := int64(rng.Intn(7) - 2) // [-2,4]
		arr := New(uint64(n), def)
		model := newRef(n, def)
		var snaps []snapPair
		for op := 0; op < 500; op++ {
			switch rng.Intn(9) {
			case 0, 1: // 点更新（含显式等于缺省的值）
				p := rng.Intn(n)
				v := int64(rng.Intn(13) - 4)
				arr.Assign(uint64(p), v)
				model.assign(p, v)
			case 2: // 范围加法
				l := rng.Intn(n)
				rr := l + rng.Intn(n-l+1)
				d := int64(rng.Intn(9) - 4)
				arr.RangeAdd(uint64(l), uint64(rr), d)
				model.rangeAdd(l, rr, d)
			case 3: // 缺省变更
				d2 := int64(rng.Intn(7) - 2)
				arr.ChangeDefault(d2)
				model.changeDefault(d2)
			case 4, 5: // 范围和
				l := rng.Intn(n)
				rr := l + rng.Intn(n-l+1)
				got := arr.RangeSum(uint64(l), uint64(rr))
				want := model.rangeSum(l, rr)
				if got != want {
					t.Fatalf("trial %d op %d: RangeSum(%d,%d)=%d want %d", trial, op, l, rr, got, want)
				}
			case 6: // 点查
				p := rng.Intn(n)
				if got, want := arr.Get(uint64(p)), model.a[p]; got != want {
					t.Fatalf("trial %d op %d: Get(%d)=%d want %d", trial, op, p, got, want)
				}
			case 7: // 快照 + 旧快照查询
				snaps = append(snaps, snapPair{arr.Snapshot(), model.clone()})
				if len(snaps) > 6 {
					snaps = snaps[1:]
				}
				k := rng.Intn(len(snaps))
				p := rng.Intn(n)
				if got, want := snaps[k].s.Get(uint64(p)), snaps[k].m.a[p]; got != want {
					t.Fatalf("trial %d op %d: snap Get(%d)=%d want %d", trial, op, p, got, want)
				}
				l := rng.Intn(n)
				rr := l + rng.Intn(n-l+1)
				if got, want := snaps[k].s.RangeSum(uint64(l), uint64(rr)), snaps[k].m.rangeSum(l, rr); got != want {
					t.Fatalf("trial %d op %d: snap RangeSum=%d want %d", trial, op, got, want)
				}
			case 8: // 累计定位（仅非负）
				if got, want := arr.NonNeg(), model.nonNeg(); got != want {
					t.Fatalf("trial %d op %d: NonNeg=%v want %v", trial, op, got, want)
				}
				if !model.nonNeg() {
					if _, ok := arr.Locate(1); ok {
						t.Fatalf("trial %d op %d: Locate allowed on negative array", trial, op)
					}
					break
				}
				total := model.rangeSum(0, n)
				if total < 1 {
					break
				}
				cum := 1 + rng.Int63n(total)
				gotP, gotOK := arr.Locate(cum)
				wantP, wantOK := model.locate(cum)
				if gotP != wantP || gotOK != wantOK {
					t.Fatalf("trial %d op %d: Locate(%d)=(%d,%v) want (%d,%v)", trial, op, cum, gotP, gotOK, wantP, wantOK)
				}
			}
		}
		// 终态全量比对
		for p := 0; p < n; p++ {
			if got, want := arr.Get(uint64(p)), model.a[p]; got != want {
				t.Fatalf("trial %d final: Get(%d)=%d want %d", trial, p, got, want)
			}
		}
		if got, want := arr.RangeSum(0, uint64(n)), model.rangeSum(0, n); got != want {
			t.Fatalf("trial %d final: total=%d want %d", trial, got, want)
		}
	}
}

// ---------- 定向语义测试 ----------

func TestExplicitZeroIsNotMissing(t *testing.T) {
	a := New(10, 0)
	a.Assign(3, 0) // 显式 0：是例外，不是缺失
	if a.Exceptions() != 1 {
		t.Fatalf("explicit 0 must be stored as exception, got %d", a.Exceptions())
	}
	a.ChangeDefault(2)
	if got := a.Get(3); got != 0 {
		t.Fatalf("explicit 0 must survive default change, got %d", got)
	}
	if got := a.Get(4); got != 2 {
		t.Fatalf("untouched position must follow new default, got %d", got)
	}
}

func TestDefaultChangeFollowAndReclaim(t *testing.T) {
	a := New(10, 0)
	a.Assign(5, 7)
	a.ChangeDefault(7) // 例外 7 == 新缺省 7 → 回收
	if a.Exceptions() != 0 {
		t.Fatalf("exception equal to new default must be reclaimed, got %d", a.Exceptions())
	}
	a.ChangeDefault(3) // 回收后位置 5 重新跟随
	if got := a.Get(5); got != 3 {
		t.Fatalf("reclaimed position must follow defaults again, got %d", got)
	}
	// 不等于新缺省的例外保留
	a.Assign(8, 1)
	a.ChangeDefault(9)
	if got := a.Get(8); got != 1 {
		t.Fatalf("exception != new default must be kept, got %d", got)
	}
	if a.Exceptions() != 1 {
		t.Fatalf("kept exception count = %d, want 1", a.Exceptions())
	}
}

func TestRangeSumIncludesDefaultContribution(t *testing.T) {
	a := New(1000, 2)
	a.Assign(10, 5)
	if got, want := a.RangeSum(0, 1000), int64(2*1000-2+5); got != want {
		t.Fatalf("RangeSum=%d want %d (default contribution missing?)", got, want)
	}
	a.ChangeDefault(4) // 缺省 2→4：未固定位置跟随
	if got, want := a.RangeSum(0, 1000), int64(4*1000-4+5); got != want {
		t.Fatalf("after ChangeDefault RangeSum=%d want %d", got, want)
	}
}

func TestMixedRangeAddAndAssignLazyLayers(t *testing.T) {
	a := New(100, 0)
	a.RangeAdd(0, 50, 3)
	a.Assign(10, 100)
	a.RangeAdd(0, 50, 1) // 赋值点也被后续范围加法推动
	if got := a.Get(10); got != 101 {
		t.Fatalf("Get(10)=%d want 101", got)
	}
	if got := a.Get(20); got != 4 {
		t.Fatalf("Get(20)=%d want 4", got)
	}
	if got := a.Get(60); got != 0 {
		t.Fatalf("Get(60)=%d want 0", got)
	}
	if got, want := a.RangeSum(0, 50), int64(49*4+101); got != want {
		t.Fatalf("RangeSum=%d want %d", got, want)
	}
}

func TestLocateNonNegativeOnly(t *testing.T) {
	a := New(10, 0)
	a.Assign(2, 5)
	a.Assign(5, 3)
	cases := []struct {
		cum  int64
		want uint64
		ok   bool
	}{{1, 2, true}, {5, 2, true}, {6, 5, true}, {8, 5, true}, {9, 0, false}, {0, 0, false}}
	for _, c := range cases {
		p, ok := a.Locate(c.cum)
		if p != c.want || ok != c.ok {
			t.Fatalf("Locate(%d)=(%d,%v) want (%d,%v)", c.cum, p, ok, c.want, c.ok)
		}
	}
	a.Assign(1, -1) // 引入负值 → 定位关闭
	if a.NonNeg() {
		t.Fatal("NonNeg must be false after negative assign")
	}
	if _, ok := a.Locate(1); ok {
		t.Fatal("Locate must be closed on negative array")
	}
}

func TestSnapshotIsolation(t *testing.T) {
	a := New(100, 1)
	a.Assign(7, 9)
	snap := a.Snapshot()
	a.Assign(7, 100)
	a.RangeAdd(0, 100, 5)
	a.ChangeDefault(42)
	if got := snap.Get(7); got != 9 {
		t.Fatalf("snapshot Get(7)=%d want 9", got)
	}
	if got := snap.RangeSum(0, 100); got != 99*1+9 {
		t.Fatalf("snapshot RangeSum=%d want %d", got, 99*1+9)
	}
	if got := a.Get(7); got != 105 {
		t.Fatalf("head Get(7)=%d want 105", got)
	}
	if got := a.Get(50); got != 47 { // 42 + 5（惰性层增量保留）
		t.Fatalf("head Get(50)=%d want 47", got)
	}
}

// ---------- 物理布局与复杂度证据 ----------

func TestPhysicalLayoutExactBytes(t *testing.T) {
	a := New(1<<40, 0)
	a.Assign(1000, 1)
	a.Assign(1001, -2)
	a.Assign(1002, 3)
	a.flush() // 白盒：强制落盘到压缩 run
	// 头16 + run头16 + 目录8 + 块(头10 + posEnc[0,1,1]=3 + valEnc[zigzag:2,3,6]=3)
	want := int64(16 + 16 + 8 + 10 + 3 + 3)
	if got := a.PhysicalBytes(); got != want {
		t.Fatalf("PhysicalBytes=%d want %d", got, want)
	}
	if got, want := a.LogicalBytes(), int64(8<<40); got != want {
		t.Fatalf("LogicalBytes=%d want %d", got, want)
	}
	t.Logf("layout: %d physical bytes vs %d logical bytes (%.2e x)",
		a.PhysicalBytes(), a.LogicalBytes(), float64(a.LogicalBytes())/float64(a.PhysicalBytes()))
}

func TestLargeLogicalLengthEvidence(t *testing.T) {
	const n = uint64(1) << 40 // 约 1.1e12 逻辑长度
	rng := rand.New(rand.NewSource(7))
	a := New(n, 0)

	t0 := time.Now()
	for i := 0; i < 20000; i++ {
		a.Assign(rng.Uint64()%n, int64(rng.Intn(1000)))
	}
	dAssign := time.Since(t0)

	t0 = time.Now()
	for i := 0; i < 2000; i++ {
		l := rng.Uint64() % (n - 1)
		r := min(n, l+1+rng.Uint64()%(1<<30)) // 区间最宽约 1e9
		a.RangeAdd(l, r, int64(rng.Intn(10)))
	}
	dAdd := time.Since(t0)

	t0 = time.Now()
	var checksum int64
	for i := 0; i < 10000; i++ {
		l := rng.Uint64() % (n - 1)
		r := min(n, l+1+rng.Uint64()%(1<<30))
		checksum += a.RangeSum(l, r)
	}
	dSum := time.Since(t0)

	t0 = time.Now()
	for i := 0; i < 200; i++ {
		a.Locate(1 + rng.Int63n(1<<40))
	}
	dLoc := time.Since(t0)

	phys := a.PhysicalBytes()
	t.Logf("n=2^40 exceptions=%d segs=%d physical=%dB logical=%dB ratio=%.2e",
		a.Exceptions(), len(a.v.segs), phys, a.LogicalBytes(), float64(a.LogicalBytes())/float64(phys))
	t.Logf("20000 Assign: %v (%.0f/op)", dAssign, float64(dAssign)/20000)
	t.Logf("2000 RangeAdd(宽至1e9): %v (%.0f/op)", dAdd, float64(dAdd)/2000)
	t.Logf("10000 RangeSum(宽至1e9): %v (%.0f/op) checksum=%d", dSum, float64(dSum)/10000, checksum)
	t.Logf("200 Locate: %v (%.0f/op)", dLoc, float64(dLoc)/200)

	if phys > 1<<20 {
		t.Fatalf("physical bytes %d exceed 1MiB for 20k exceptions", phys)
	}
	if checksum == 0 {
		t.Fatal("checksum should be nonzero")
	}
}
