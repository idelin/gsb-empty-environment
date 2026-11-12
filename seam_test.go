package taskpkg

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// refEnergy 独立实现一份与生产代码相同定义的单像素代价，
// 供全路径枚举基线使用，避免直接复用被测函数。
func refEnergy(a, b *Frame, x, y int, prevX int, temporalWeight float64, hasPrev bool) float64 {
	width, height := a.Width, a.Height
	xl := max(x-1, 0)
	xr := min(x+1, width-1)
	yu := max(y-1, 0)
	yd := min(y+1, height-1)
	var cost float64
	for c := 0; c < 3; c++ {
		d := channelAt(a, x, y, c) - channelAt(b, x, y, c)
		cost += d * d
		gax := channelAt(a, xr, y, c) - channelAt(a, xl, y, c)
		gbx := channelAt(b, xr, y, c) - channelAt(b, xl, y, c)
		d = gax - gbx
		cost += d * d
		gay := channelAt(a, x, yd, c) - channelAt(a, x, yu, c)
		gby := channelAt(b, x, yd, c) - channelAt(b, x, yu, c)
		d = gay - gby
		cost += d * d
	}
	if hasPrev {
		cost += temporalWeight * math.Abs(float64(x-prevX))
	}
	return cost
}

func refSeamCost(a, b *Frame, m *Mask, seam []int, temporalWeight float64, hasPrev bool, prevSeam []int) float64 {
	var total float64
	for y, x := range seam {
		prevX := 0
		if hasPrev {
			prevX = prevSeam[y]
		}
		total += refEnergy(a, b, x, y, prevX, temporalWeight, hasPrev)
	}
	return total
}

// enumerateSeams 深度优先枚举所有满足约束的缝合线。
func enumerateSeams(m *Mask) [][]int {
	width, height := m.Width, m.Height
	var results [][]int
	path := make([]int, height)
	var dfs func(y int)
	dfs = func(y int) {
		if y == height {
			seam := append([]int(nil), path...)
			results = append(results, seam)
			return
		}
		for x := 0; x < width; x++ {
			if !m.At(x, y) {
				continue
			}
			if y > 0 && absInt(x-path[y-1]) > 1 {
				continue
			}
			path[y] = x
			dfs(y + 1)
		}
	}
	dfs(0)
	return results
}

func lexLess(s1, s2 []int) bool {
	for i := range s1 {
		if s1[i] != s2[i] {
			return s1[i] < s2[i]
		}
	}
	return false
}

// bruteForceBest 在全枚举结果上求最小代价；代价并列时取字典序最小。
func bruteForceBest(a, b *Frame, m *Mask, prevSeam []int, weight float64, hasPrev bool) ([]int, float64) {
	seams := enumerateSeams(m)
	if len(seams) == 0 {
		return nil, math.Inf(1)
	}
	bestSeam := seams[0]
	bestCost := refSeamCost(a, b, m, bestSeam, weight, hasPrev, prevSeam)
	for _, s := range seams[1:] {
		cost := refSeamCost(a, b, m, s, weight, hasPrev, prevSeam)
		if cost < bestCost || (cost == bestCost && lexLess(s, bestSeam)) {
			bestCost = cost
			bestSeam = s
		}
	}
	return bestSeam, bestCost
}

func makeRandomCase(rng *rand.Rand) (*Frame, *Frame, *Mask) {
	width := 1 + rng.Intn(5)
	height := 1 + rng.Intn(5)
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range a.Pix {
		a.Pix[i] = float64(rng.Intn(5))
		b.Pix[i] = float64(rng.Intn(5))
	}
	for i := range m.Pix {
		m.Pix[i] = rng.Float64() < 0.7
	}
	return a, b, m
}

// TestExhaustiveSmallGrids 在随机小图上枚举全部合法缝，逐一对账
// 动态规划输出的最优代价与字典序平局选择，覆盖空间域与时间域。
func TestExhaustiveSmallGrids(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for trial := 0; trial < 300; trial++ {
		a, b, m := makeRandomCase(rng)
		all := enumerateSeams(m)

		// 空间域：无时间惩罚。
		got, err := Find(a, b, m, nil, nil)
		if len(all) == 0 {
			if !errors.Is(err, ErrDisconnected) {
				t.Fatalf("trial %d: 期望 ErrDisconnected，得到 err=%v", trial, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: 意外报错 %v", trial, err)
		}
		wantSeam, wantCost := bruteForceBest(a, b, m, nil, 0, false)
		if !equalInts(got, wantSeam) {
			t.Fatalf("trial %d: 空间域缝不一致 got=%v want=%v", trial, got, wantSeam)
		}
		gotCost := refSeamCost(a, b, m, got, 0, false, nil)
		if gotCost != wantCost {
			t.Fatalf("trial %d: 空间域代价不一致 got=%v want=%v", trial, gotCost, wantCost)
		}

		// 时间域：提供前一帧缝，分别用默认权重与显式权重。
		prev := make([]int, m.Height)
		for y := range prev {
			prev[y] = rng.Intn(m.Width)
		}
		for _, weight := range []float64{1.0, 0.25} {
			var opts *Options
			var usedWeight float64
			if weight == 1.0 {
				opts = nil
				usedWeight = 1.0
			} else {
				opts = &Options{TemporalWeight: weight}
				usedWeight = weight
			}
			gotT, err := Find(a, b, m, prev, opts)
			if err != nil {
				t.Fatalf("trial %d weight %v: 意外报错 %v", trial, weight, err)
			}
			wantT, wantTCost := bruteForceBest(a, b, m, prev, usedWeight, true)
			if !equalInts(gotT, wantT) {
				t.Fatalf("trial %d weight %v: 时间域缝不一致 got=%v want=%v", trial, weight, gotT, wantT)
			}
			gotTCost := refSeamCost(a, b, m, gotT, usedWeight, true, prev)
			if gotTCost != wantTCost {
				t.Fatalf("trial %d weight %v: 时间域代价不一致 got=%v want=%v", trial, weight, gotTCost, wantTCost)
			}
		}

		// 显式零权重应与空间域结果完全一致。
		gotZero, err := Find(a, b, m, prev, &Options{TemporalWeight: 0})
		if err != nil || !equalInts(gotZero, wantSeam) {
			t.Fatalf("trial %d: 零时间权重结果异常 got=%v err=%v", trial, gotZero, err)
		}
	}
}

func equalInts(s1, s2 []int) bool {
	if len(s1) != len(s2) {
		return false
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return false
		}
	}
	return true
}

// TestLexicographicTie 构造全部像素颜色与梯度完全相同的输入，
// 此时所有合法缝代价相等，必须返回字典序最小的一条。
func TestLexicographicTie(t *testing.T) {
	width, height := 4, 4
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	for i := range a.Pix {
		a.Pix[i] = 0.3
		b.Pix[i] = 0.3
	}
	m := NewMask(width, height)
	for i := range m.Pix {
		m.Pix[i] = true
	}
	// 留出一些无效像素，确保枚举空间非平凡，同时保持存在贯穿组件。
	m.Set(0, 0, false)
	m.Set(3, 3, false)

	got, err := Find(a, b, m, nil, nil)
	if err != nil {
		t.Fatalf("意外报错 %v", err)
	}
	want, _ := bruteForceBest(a, b, m, nil, 0, false)
	if !equalInts(got, want) {
		t.Fatalf("平局字典序选择错误 got=%v want=%v", got, want)
	}

	// 字典序最小缝应尽可能靠左：逐行验证其就是最左可达选择。
	seams := enumerateSeams(m)
	for _, s := range seams {
		if lexLess(s, got) {
			t.Fatalf("存在字典序更小的缝 %v，却返回 %v", s, got)
		}
	}
}

// TestTemporalPullsSeam 验证时间惩罚会把缝拉向前一帧的位置。
func TestTemporalPullsSeam(t *testing.T) {
	width, height := 3, 3
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range m.Pix {
		m.Pix[i] = true
	}
	// 空间域所有像素代价相同，最左缝 [0 0 0] 为字典序最优。
	prev := []int{2, 2, 2}
	got, err := Find(a, b, m, prev, &Options{TemporalWeight: 10})
	if err != nil {
		t.Fatalf("意外报错 %v", err)
	}
	want, _ := bruteForceBest(a, b, m, prev, 10, true)
	if !equalInts(got, want) {
		t.Fatalf("时间域最优缝错误 got=%v want=%v", got, want)
	}
	// 强时间惩罚下最优缝应贴到最右列。
	for y, x := range got {
		if x != 2 {
			t.Fatalf("第 %d 行期望被拉到列 2，实际列 %d（缝=%v）", y, x, got)
		}
	}
}

// TestDisconnectedMask 覆盖多种不连通情形。
func TestDisconnectedMask(t *testing.T) {
	a := NewFrame(3, 4)
	b := NewFrame(3, 4)

	// 情形一：某一行完全没有有效像素。
	emptyRow := NewMask(3, 4)
	for x := 0; x < 3; x++ {
		for y := 0; y < 4; y++ {
			if y != 2 {
				emptyRow.Set(x, y, true)
			}
		}
	}
	if _, err := Find(a, b, emptyRow, nil, nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("空行情形期望 ErrDisconnected，得到 %v", err)
	}

	// 情形二：上下两块有效区域被整行无效带隔开。
	split := NewMask(3, 4)
	for x := 0; x < 3; x++ {
		split.Set(x, 0, true)
		split.Set(x, 1, true)
		split.Set(x, 3, true)
	}
	if _, err := Find(a, b, split, nil, nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("分离块情形期望 ErrDisconnected，得到 %v", err)
	}

	// 情形三：对角像素仅 4 连通、不满足 8 连通贯穿。
	da, db := NewFrame(3, 3), NewFrame(3, 3)
	diagonal := NewMask(3, 3)
	diagonal.Set(0, 0, true)
	diagonal.Set(2, 1, true)
	diagonal.Set(0, 2, true)
	if _, err := Find(da, db, diagonal, nil, nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("对角断裂情形期望 ErrDisconnected，得到 %v", err)
	}

	// 对照：对角相邻在 8 连通下合法，应成功。
	ca, cb := NewFrame(3, 3), NewFrame(3, 3)
	connected8 := NewMask(3, 3)
	connected8.Set(0, 0, true)
	connected8.Set(1, 1, true)
	connected8.Set(2, 2, true)
	seam, err := Find(ca, cb, connected8, nil, nil)
	if err != nil {
		t.Fatalf("8 连通对照意外报错 %v", err)
	}
	if !equalInts(seam, []int{0, 1, 2}) {
		t.Fatalf("8 连通对照缝错误 got=%v", seam)
	}
}

func TestValidationErrors(t *testing.T) {
	a := NewFrame(3, 3)
	b := NewFrame(3, 3)
	m := NewMask(3, 3)
	for i := range m.Pix {
		m.Pix[i] = true
	}

	if _, err := Find(nil, b, m, nil, nil); err == nil {
		t.Fatal("nil 帧应报错")
	}
	if _, err := Find(a, NewFrame(2, 3), m, nil, nil); err == nil {
		t.Fatal("尺寸不一致应报错")
	}
	if _, err := Find(a, b, m, []int{0, 0}, nil); err == nil {
		t.Fatal("prevSeam 长度不符应报错")
	}
	if _, err := Find(a, b, m, []int{0, 9, 0}, nil); err == nil {
		t.Fatal("prevSeam 越界应报错")
	}

	good := []int{0, 1, 1}
	if _, err := Feather(a, b, m, good, 1); err != nil {
		t.Fatalf("合法缝羽化意外报错 %v", err)
	}
	if _, err := Feather(a, b, m, []int{0, 2, 1}, 1); err == nil {
		t.Fatal("横移超过 1 的缝应报错")
	}
	if _, err := Feather(a, b, m, []int{0, 1, 0}, -1); err == nil {
		t.Fatal("负半宽应报错")
	}
}
