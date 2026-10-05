package main

import (
	"fmt"
	"testing"
)

// allSeqs 生成 alphabet 上长度 0..maxLen 的全部序列。
func allSeqs(alphabet []string, maxLen int) [][]string {
	seqs := [][]string{{}}
	cur := [][]string{{}}
	for l := 0; l < maxLen; l++ {
		var next [][]string
		for _, s := range cur {
			for _, ch := range alphabet {
				ns := append(append([]string(nil), s...), ch)
				next = append(next, ns)
				seqs = append(seqs, ns)
			}
		}
		cur = next
	}
	return seqs
}

func resolvedLines(segs []Segment) ([]string, bool) {
	var out []string
	ok := true
	for _, s := range segs {
		if s.Conflict {
			ok = false
			continue
		}
		out = append(out, s.Lines...)
	}
	return out, ok
}

func eq(a, b []string) bool { return equalLines(a, b) }

// checkChunkInvariants 校验：三方区间分别完整划分各自序列、稳定/变更交替
// （变更块最小且互不重叠）、变更块端点确实不稳定、区间合法。
func checkChunkInvariants(t *testing.T, base, left, right []string) {
	n := len(base)
	ld := diffSide(base, left)
	rd := diffSide(base, right)
	stableLine := func(i int) bool { return ld.keep[i] && rd.keep[i] }
	chunks := buildChunks(base, left, right)
	bpos, lpos, rpos := 0, 0, 0
	prevChanged := false
	for k, c := range chunks {
		if c.BaseLo != bpos || c.LeftLo != lpos || c.RightLo != rpos {
			t.Fatalf("块 %d 未紧接前一块: %+v, 期望起点 (%d,%d,%d)", k, c, bpos, lpos, rpos)
		}
		if c.BaseLo > c.BaseHi || c.BaseHi > n ||
			c.LeftLo > c.LeftHi || c.LeftHi > len(left) ||
			c.RightLo > c.RightHi || c.RightHi > len(right) {
			t.Fatalf("块 %d 区间非法: %+v", k, c)
		}
		if c.Stable {
			if c.BaseHi-c.BaseLo != c.LeftHi-c.LeftLo ||
				c.BaseHi-c.BaseLo != c.RightHi-c.RightLo {
				t.Fatalf("稳定块 %d 三方长度不一致: %+v", k, c)
			}
			for i := c.BaseLo; i < c.BaseHi; i++ {
				if !stableLine(i) {
					t.Fatalf("稳定块 %d 内含不稳定行 %d", k, i)
				}
			}
			prevChanged = false
		} else {
			if prevChanged {
				t.Fatalf("块 %d 与前一变更块相邻（应合并）: %+v", k, c)
			}
			if c.BaseLo == c.BaseHi && c.LeftLo == c.LeftHi && c.RightLo == c.RightHi {
				t.Fatalf("块 %d 为全空变更块", k)
			}
			if c.BaseLo < c.BaseHi {
				if stableLine(c.BaseLo) {
					t.Fatalf("变更块 %d 首行 %d 实际稳定（块非最小）: %+v", k, c.BaseLo, c)
				}
				if stableLine(c.BaseHi - 1) {
					t.Fatalf("变更块 %d 末行 %d 实际稳定（块非最小）: %+v", k, c.BaseHi-1, c)
				}
			}
			prevChanged = true
		}
		bpos, lpos, rpos = c.BaseHi, c.LeftHi, c.RightHi
	}
	if bpos != n || lpos != len(left) || rpos != len(right) {
		t.Fatalf("块未完整划分三方: 结束于 (%d,%d,%d), 期望 (%d,%d,%d)",
			bpos, lpos, rpos, n, len(left), len(right))
	}
}

// checkMergeInvariants 对单个三元组校验全部合并不变式。
func checkMergeInvariants(t *testing.T, base, left, right []string) {
	checkChunkInvariants(t, base, left, right)

	segs := Merge(base, left, right)

	// 冲突块内容必须三方两两不同（否则应已被自动合并）
	for _, s := range segs {
		if !s.Conflict {
			continue
		}
		if eq(s.Left, s.Right) || eq(s.Left, s.Base) || eq(s.Right, s.Base) {
			t.Fatalf("冲突块本可自动合并: base=%v left=%v right=%v", base, left, right)
		}
	}

	// 对称性：交换左右两侧，结果必须镜像相同（无修改顺序偏向）
	segs2 := Merge(base, right, left)
	if len(segs) != len(segs2) {
		t.Fatalf("交换两侧后分段数不同: base=%v left=%v right=%v", base, left, right)
	}
	for i := range segs {
		a, b := segs[i], segs2[i]
		if a.Conflict != b.Conflict {
			t.Fatalf("交换两侧后冲突位置不同: base=%v left=%v right=%v", base, left, right)
		}
		if !a.Conflict {
			if !eq(a.Lines, b.Lines) {
				t.Fatalf("交换两侧后已解析内容不同: base=%v left=%v right=%v", base, left, right)
			}
			continue
		}
		if !eq(a.Base, b.Base) || !eq(a.Left, b.Right) || !eq(a.Right, b.Left) ||
			a.Chunk.BaseLo != b.Chunk.BaseLo || a.Chunk.BaseHi != b.Chunk.BaseHi ||
			a.Chunk.LeftLo != b.Chunk.RightLo || a.Chunk.LeftHi != b.Chunk.RightHi ||
			a.Chunk.RightLo != b.Chunk.LeftLo || a.Chunk.RightHi != b.Chunk.LeftHi {
			t.Fatalf("交换两侧后冲突不镜像: base=%v left=%v right=%v", base, left, right)
		}
	}

	// 两侧相同修改 -> 原样输出一次，无冲突
	if eq(left, right) {
		out, clean := resolvedLines(segs)
		if !clean || !eq(out, left) {
			t.Fatalf("两侧相同未原样输出: base=%v left=%v", base, left)
		}
	}
	// 仅一侧修改 -> 输出该侧，无冲突
	if eq(left, base) {
		out, clean := resolvedLines(segs)
		if !clean || !eq(out, right) {
			t.Fatalf("左侧未改但输出不等于右侧: base=%v right=%v", base, right)
		}
	}
	if eq(right, base) {
		out, clean := resolvedLines(segs)
		if !clean || !eq(out, left) {
			t.Fatalf("右侧未改但输出不等于左侧: base=%v left=%v", base, left)
		}
	}
}

func TestExhaustiveBinaryLen4(t *testing.T) {
	seqs := allSeqs([]string{"a", "b"}, 4)
	for _, base := range seqs {
		for _, left := range seqs {
			for _, right := range seqs {
				checkMergeInvariants(t, base, left, right)
			}
		}
	}
}

func TestExhaustiveTernaryLen3(t *testing.T) {
	seqs := allSeqs([]string{"a", "b", "c"}, 3)
	for _, base := range seqs {
		for _, left := range seqs {
			for _, right := range seqs {
				checkMergeInvariants(t, base, left, right)
			}
		}
	}
}

func mustMerge(t *testing.T, base, left, right []string) ([]string, []Segment) {
	t.Helper()
	segs := Merge(base, left, right)
	out, clean := resolvedLines(segs)
	if !clean {
		t.Fatalf("期望无冲突: base=%v left=%v right=%v", base, left, right)
	}
	return out, segs
}

func TestRepeatedLinesMatchByPosition(t *testing.T) {
	// base 中 x 重复出现；左删第一个 x、右删最后一个 x，
	// 必须按序列位置对齐，结果去掉首尾两个 x。
	base := []string{"x", "a", "x", "a", "x"}
	left := []string{"a", "x", "a", "x"}
	right := []string{"x", "a", "x", "a"}
	out, _ := mustMerge(t, base, left, right)
	if !eq(out, []string{"a", "x", "a"}) {
		t.Fatalf("重复行位置匹配错误: got %v", out)
	}
}

func TestIndependentEditsAutoMerge(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	left := []string{"A", "b", "c", "d"}
	right := []string{"a", "b", "c", "D"}
	out, _ := mustMerge(t, base, left, right)
	if !eq(out, []string{"A", "b", "c", "D"}) {
		t.Fatalf("独立修改未自动合并: got %v", out)
	}
}

func TestSameChangeKeptOnce(t *testing.T) {
	base := []string{"a", "b"}
	both := []string{"a", "X", "b"}
	out, _ := mustMerge(t, base, both, both)
	if !eq(out, both) {
		t.Fatalf("相同修改未只保留一次: got %v", out)
	}
}

func TestIdenticalInsertAtSameGap(t *testing.T) {
	base := []string{"a", "b"}
	both := []string{"a", "n1", "n2", "b"}
	out, _ := mustMerge(t, base, both, both)
	if !eq(out, both) {
		t.Fatalf("同隙相同插入未去重: got %v", out)
	}
}

func TestConflictRangesAndMinimality(t *testing.T) {
	base := []string{"a", "b", "c"}
	left := []string{"a", "L", "c"}
	right := []string{"a", "R", "c"}
	segs := Merge(base, left, right)
	var conflicts []Segment
	for _, s := range segs {
		if s.Conflict {
			conflicts = append(conflicts, s)
		}
	}
	if len(conflicts) != 1 {
		t.Fatalf("期望恰好 1 个冲突块, got %d", len(conflicts))
	}
	c := conflicts[0].Chunk
	if c.BaseLo != 1 || c.BaseHi != 2 || c.LeftLo != 1 || c.LeftHi != 2 || c.RightLo != 1 || c.RightHi != 2 {
		t.Fatalf("冲突区间错误: %+v", c)
	}
	if !eq(conflicts[0].Base, []string{"b"}) || !eq(conflicts[0].Left, []string{"L"}) || !eq(conflicts[0].Right, []string{"R"}) {
		t.Fatalf("冲突内容错误: %+v", conflicts[0])
	}
}

func TestConflictEmptyBaseRange(t *testing.T) {
	// 两侧在同一间隙插入不同内容：冲突的 base 区间为空
	base := []string{"a", "b"}
	left := []string{"a", "L", "b"}
	right := []string{"a", "R", "b"}
	segs := Merge(base, left, right)
	found := false
	for _, s := range segs {
		if s.Conflict {
			found = true
			if s.Chunk.BaseLo != s.Chunk.BaseHi {
				t.Fatalf("纯插入冲突的 base 区间应为空: %+v", s.Chunk)
			}
		}
	}
	if !found {
		t.Fatal("期望产生冲突")
	}
}

func TestRenderContainsRanges(t *testing.T) {
	base := []string{"a", "b", "c"}
	left := []string{"a", "L", "c"}
	right := []string{"a", "R", "c"}
	text, hasConflict := render(Merge(base, left, right), "BASE", "LEFT", "RIGHT")
	if !hasConflict {
		t.Fatal("期望有冲突")
	}
	for _, want := range []string{"BASE: 第 2-2 行", "LEFT: 第 2-2 行", "RIGHT: 第 2-2 行"} {
		if !contains(text, want) {
			t.Fatalf("渲染缺少来源区间 %q:\n%s", want, text)
		}
	}
	fmt.Println(text)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
