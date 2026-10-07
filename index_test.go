package intervalidx

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func rat(n int64) *big.Rat { return big.NewRat(n, 1) }

func segStrings(ix *Index, v int) []string {
	var out []string
	for _, s := range ix.Segments(v) {
		out = append(out, fmt.Sprintf("[%s,%s)=%v", s.Start.RatString(), s.End.RatString(), s.Value))
	}
	return out
}

func assertSegs(t *testing.T, ix *Index, v int, want ...string) {
	t.Helper()
	got := segStrings(ix, v)
	if len(got) != len(want) {
		t.Fatalf("version %d: got %v, want %v", v, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("version %d: got %v, want %v", v, got, want)
		}
	}
}

func assertPoint(t *testing.T, ix *Index, v int, p *big.Rat, want interface{}, wantOK bool) {
	t.Helper()
	got, ok := ix.Get(v, p)
	if ok != wantOK || (wantOK && got != want) {
		t.Fatalf("Get(v=%d, %s) = (%v,%v), want (%v,%v)", v, p.RatString(), got, ok, want, wantOK)
	}
}

// 覆盖：整域赋值后逐点可查。
func TestCoverAndQuery(t *testing.T) {
	ix := New()
	v := ix.Set(rat(0), rat(10), "A")
	assertSegs(t, ix, v, "[0,10)=A")
	assertPoint(t, ix, v, rat(0), "A", true)
	assertPoint(t, ix, v, rat(9), "A", true)
	assertPoint(t, ix, v, rat(10), nil, false) // 半开：右端点不含
	assertPoint(t, ix, v, rat(-1), nil, false) // 域外
}

// 部分删除：中段删除把旧段分裂为两段，且不相邻不合并。
func TestPartialDeleteSplits(t *testing.T) {
	ix := New()
	ix.Set(rat(0), rat(10), "A")
	v := ix.Delete(rat(3), rat(5))
	assertSegs(t, ix, v, "[0,3)=A", "[5,10)=A")
	assertPoint(t, ix, v, rat(4), nil, false)
	assertPoint(t, ix, v, rat(3), nil, false)
	assertPoint(t, ix, v, rat(5), "A", true)
	// 旧快照仍保留原段。
	assertSegs(t, ix, v-1, "[0,10)=A")
	assertPoint(t, ix, v-1, rat(4), "A", true)
}

// 分裂后合并：先以异值覆盖中段造成三段，再回填同值，应合并回单段。
func TestSplitThenMerge(t *testing.T) {
	ix := New()
	ix.Set(rat(0), rat(10), "A")
	v1 := ix.Set(rat(3), rat(5), "B")
	assertSegs(t, ix, v1, "[0,3)=A", "[3,5)=B", "[5,10)=A")
	v2 := ix.Set(rat(3), rat(5), "A")
	assertSegs(t, ix, v2, "[0,10)=A")
	// 相邻同值跨多次更新也应合并。
	v3 := ix.Set(rat(10), rat(12), "A")
	assertSegs(t, ix, v3, "[0,12)=A")
}

// 空范围不产生新版本。
func TestEmptyRangeNoVersion(t *testing.T) {
	ix := New()
	if v := ix.Set(rat(3), rat(3), "A"); v != 0 || ix.NumVersions() != 1 {
		t.Fatalf("empty Set created version: v=%d n=%d", v, ix.NumVersions())
	}
	if v := ix.Set(rat(5), rat(2), "A"); v != 0 || ix.NumVersions() != 1 {
		t.Fatalf("reversed Set created version: v=%d n=%d", v, ix.NumVersions())
	}
	ix.Set(rat(0), rat(4), "A")
	if v := ix.Delete(rat(2), rat(2)); v != 1 || ix.NumVersions() != 2 {
		t.Fatalf("empty Delete created version: v=%d n=%d", v, ix.NumVersions())
	}
}

// 有理端点：边界落在整数之间，查询与分裂均以有理数比较。
func TestRationalEndpoints(t *testing.T) {
	ix := New()
	half := big.NewRat(1, 2)
	v := ix.Set(big.NewRat(1, 2), big.NewRat(5, 2), "A") // [1/2, 5/2)
	assertSegs(t, ix, v, "[1/2,5/2)=A")
	assertPoint(t, ix, v, big.NewRat(1, 3), nil, false)
	assertPoint(t, ix, v, half, "A", true)
	assertPoint(t, ix, v, rat(2), "A", true)
	assertPoint(t, ix, v, big.NewRat(5, 2), nil, false)
	// 用有理边界部分删除，旧段在分数处分裂。
	v2 := ix.Delete(big.NewRat(3, 2), big.NewRat(7, 4)) // 删 [3/2, 7/4)
	assertSegs(t, ix, v2, "[1/2,3/2)=A", "[7/4,5/2)=A")
	assertPoint(t, ix, v2, big.NewRat(8, 5), nil, false) // 8/5 在被删区间
	assertPoint(t, ix, v2, big.NewRat(7, 4), "A", true)
}

// 逐整数点快照模型：随机操作序列下，每个版本都与整数点模型快照一致。
func TestModelIntegerPointSnapshots(t *testing.T) {
	const (
		domain = 32   // 整数点 0..domain-1
		steps  = 2000 // 随机操作数
		nvals  = 3    // 小值域，增加相邻同值合并机会
		den    = 2    // 端点分母：允许半整数边界触发整数点间分裂
	)
	rng := rand.New(rand.NewSource(42))
	ix := New()
	// model[v][p] = 版本 v 中整数点 p 的值（-1 表示无覆盖）。
	model := make([][]int, 1, steps+1)
	base := make([]int, domain)
	for i := range base {
		base[i] = -1
	}
	model[0] = base

	check := func(v int) {
		for p := 0; p < domain; p++ {
			got, ok := ix.Get(v, rat(int64(p)))
			want := model[v][p]
			if want < 0 {
				if ok {
					t.Fatalf("v=%d p=%d: got (%v,true), want uncovered", v, p, got)
				}
			} else if !ok || got != want {
				t.Fatalf("v=%d p=%d: got (%v,%v), want %d", v, p, got, ok, want)
			}
		}
		// 结构不变量：有序、互不交、相邻不同值。
		segs := ix.Segments(v)
		for i, s := range segs {
			if s.Start.Cmp(s.End) >= 0 {
				t.Fatalf("v=%d: degenerate segment %v", v, s)
			}
			if i > 0 {
				prev := segs[i-1]
				if prev.End.Cmp(s.Start) > 0 {
					t.Fatalf("v=%d: overlapping segments %v %v", v, prev, s)
				}
				if prev.End.Cmp(s.Start) == 0 && prev.Value == s.Value {
					t.Fatalf("v=%d: unmerged equal neighbors %v %v", v, prev, s)
				}
			}
		}
	}

	for step := 0; step < steps; step++ {
		// 半整数端点：k/den，范围可能为空（约 1/8 概率）。
		a, b := rng.Intn(domain*den+1), rng.Intn(domain*den+1)
		if a > b {
			a, b = b, a
		}
		if rng.Intn(8) == 0 {
			b = a // 强制空范围
		}
		start, end := big.NewRat(int64(a), den), big.NewRat(int64(b), den)
		before := ix.NumVersions()
		cur := make([]int, domain)
		copy(cur, model[ix.Latest()])
		var v int
		if rng.Intn(4) == 0 { // 1/4 删除，3/4 赋值
			v = ix.Delete(start, end)
			for p := 0; p < domain; p++ {
				if start.Cmp(rat(int64(p))) <= 0 && rat(int64(p)).Cmp(end) < 0 {
					cur[p] = -1
				}
			}
		} else {
			val := rng.Intn(nvals)
			v = ix.Set(start, end, val)
			for p := 0; p < domain; p++ {
				if start.Cmp(rat(int64(p))) <= 0 && rat(int64(p)).Cmp(end) < 0 {
					cur[p] = val
				}
			}
		}
		empty := start.Cmp(end) >= 0
		if empty {
			if ix.NumVersions() != before || v != ix.Latest() {
				t.Fatalf("step %d: empty range changed versions (n=%d->%d)", step, before, ix.NumVersions())
			}
		} else {
			if ix.NumVersions() != before+1 || v != ix.Latest() {
				t.Fatalf("step %d: version not appended (n=%d->%d, v=%d)", step, before, ix.NumVersions(), v)
			}
			model = append(model, cur)
		}
		check(ix.Latest())
		// 周期性全量核对所有历史快照。
		if step%199 == 0 {
			for ver := 0; ver < ix.NumVersions(); ver++ {
				check(ver)
			}
		}
	}
	// 收尾：全量核对所有保留快照。
	for ver := 0; ver < ix.NumVersions(); ver++ {
		check(ver)
	}
	t.Logf("checked %d versions x %d points", ix.NumVersions(), domain)
}
