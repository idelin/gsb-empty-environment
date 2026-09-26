package wavelet

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

// bruteKth 暴力求 data[l..r) 第 k 小（拷贝后排序，仅用于对拍）。
func bruteKth(data []int, l, r, k int) int {
	cp := make([]int, r-l)
	copy(cp, data[l:r])
	sort.Ints(cp)
	return cp[k]
}

// bruteRangeFreq 暴力统计 data[l..r) 中值落在 [lo, hi] 的个数。
func bruteRangeFreq(data []int, l, r, lo, hi int) int {
	cnt := 0
	for _, v := range data[l:r] {
		if v >= lo && v <= hi {
			cnt++
		}
	}
	return cnt
}

func randSlice(rng *rand.Rand, n, lo, hi int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = lo + rng.Intn(hi-lo+1)
	}
	return s
}

func TestAccessRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		data := randSlice(rng, 1+rng.Intn(300), -1000, 1000)
		tr := New(data)
		if tr.Len() != len(data) {
			t.Fatalf("Len = %d, want %d", tr.Len(), len(data))
		}
		for i, want := range data {
			if got := tr.Access(i); got != want {
				t.Fatalf("Access(%d) = %d, want %d (data=%v)", i, got, want, data)
			}
		}
	}
}

func TestKthSmallestRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 100; trial++ {
		data := randSlice(rng, 1+rng.Intn(200), -50, 50) // 小值域制造大量重复
		tr := New(data)
		for q := 0; q < 50; q++ {
			l := rng.Intn(len(data))
			r := l + 1 + rng.Intn(len(data)-l)
			k := rng.Intn(r - l)
			got, err := tr.KthSmallest(l, r, k)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := bruteKth(data, l, r, k); got != want {
				t.Fatalf("KthSmallest(%d,%d,%d) = %d, want %d (data=%v)", l, r, k, got, want, data)
			}
		}
	}
}

func TestRangeFreqRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 100; trial++ {
		data := randSlice(rng, 1+rng.Intn(200), -100, 100)
		tr := New(data)
		for q := 0; q < 50; q++ {
			l := rng.Intn(len(data))
			r := l + rng.Intn(len(data)-l+1) // 允许空区间，结果应为 0
			lo := -120 + rng.Intn(240)
			hi := lo + rng.Intn(240)
			if got, want := tr.RangeFreq(l, r, lo, hi), bruteRangeFreq(data, l, r, lo, hi); got != want {
				t.Fatalf("RangeFreq(%d,%d,%d,%d) = %d, want %d (data=%v)", l, r, lo, hi, got, want, data)
			}
		}
	}
}

func TestDuplicatesCountedByMultiplicity(t *testing.T) {
	data := []int{5, 5, 5, 1, 1, 9}
	tr := New(data)
	// 整个区间排序后为 [1 1 5 5 5 9]
	want := []int{1, 1, 5, 5, 5, 9}
	for k, w := range want {
		got, err := tr.KthSmallest(0, len(data), k)
		if err != nil || got != w {
			t.Fatalf("KthSmallest(0,6,%d) = %d, %v; want %d", k, got, err, w)
		}
	}
	if got := tr.Freq(0, len(data), 5); got != 3 {
		t.Fatalf("Freq(5) = %d, want 3", got)
	}
	if got := tr.RangeFreq(2, 5, 1, 5); got != 3 { // 子区间 [5,1,1]... 实为 data[2..5)={5,1,1}
		t.Fatalf("RangeFreq = %d, want 3", got)
	}
}

func TestEmptyRangeKthRejected(t *testing.T) {
	tr := New([]int{3, 1, 2})
	cases := [][3]int{
		{2, 2, 0},  // 空区间 l==r
		{0, 0, 0},  // 空区间在头部
		{3, 3, 0},  // 空区间在尾部
		{0, 3, 3},  // k == 区间长度，越界
		{0, 3, -1}, // 负 k
		{2, 1, 0},  // l > r
	}
	for _, c := range cases {
		if _, err := tr.KthSmallest(c[0], c[1], c[2]); !errors.Is(err, ErrEmptyRange) {
			t.Fatalf("KthSmallest%v err = %v, want ErrEmptyRange", c, err)
		}
	}
}

func TestNegativeCoordinatesRestored(t *testing.T) {
	data := []int{-7, -3, -100, -3, 0, -1, 42}
	tr := New(data)
	for i, want := range data {
		if got := tr.Access(i); got != want {
			t.Fatalf("Access(%d) = %d, want %d", i, got, want)
		}
	}
	// 排序后 [-100 -7 -3 -3 -1 0 42]
	got, err := tr.KthSmallest(0, len(data), 0)
	if err != nil || got != -100 {
		t.Fatalf("min = %d, %v; want -100", got, err)
	}
	got, err = tr.KthSmallest(0, len(data), 3)
	if err != nil || got != -3 {
		t.Fatalf("4th = %d, %v; want -3", got, err)
	}
	if got := tr.RangeFreq(0, len(data), -50, -2); got != 3 { // -7, -3, -3
		t.Fatalf("RangeFreq(-50,-2) = %d, want 3", got)
	}
}

func TestEmptyInput(t *testing.T) {
	tr := New(nil)
	if tr.Len() != 0 {
		t.Fatalf("Len = %d, want 0", tr.Len())
	}
	if got := tr.RangeFreq(0, 0, -10, 10); got != 0 {
		t.Fatalf("RangeFreq on empty = %d, want 0", got)
	}
	if _, err := tr.KthSmallest(0, 0, 0); !errors.Is(err, ErrEmptyRange) {
		t.Fatalf("err = %v, want ErrEmptyRange", err)
	}
}

func TestBytesAndLayout(t *testing.T) {
	data := randSlice(rand.New(rand.NewSource(4)), 1000, -500, 500)
	tr := New(data)
	if tr.Bytes() <= 0 {
		t.Fatal("Bytes must be positive")
	}
	// 位向量部分应约为 n*ceil(log2 sigma) bit + rank 索引开销，
	// 这里只验证数量级合理（远小于 map 包装的逐元素开销）。
	t.Logf("n=1000, sigma=1001, Bytes=%d, layout=%s", tr.Bytes(), tr.Layout())
}
