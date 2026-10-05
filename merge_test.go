package main

import (
	"reflect"
	"testing"
)

// cleanLines 把输出段拼成最终文本行；若有冲突则 ok=false。
func cleanLines(segs []Segment) (lines []string, ok bool) {
	for _, s := range segs {
		if s.Conflict != nil {
			return nil, false
		}
		lines = append(lines, s.Lines...)
	}
	return lines, true
}

func TestIndependentChangesMerge(t *testing.T) {
	base := []string{"a", "b", "c", "d", "e"}
	left := []string{"A", "b", "c", "d", "e"}
	right := []string{"a", "b", "c", "d", "E"}
	got, ok := cleanLines(Merge(base, left, right))
	if !ok {
		t.Fatal("独立修改不应产生冲突")
	}
	want := []string{"A", "b", "c", "d", "E"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAdjacentChangesMerge(t *testing.T) {
	// 两侧修改的行紧紧相邻（无稳定行间隔），仍属独立修改，应自动合并。
	base := []string{"a", "b"}
	left := []string{"A", "b"}
	right := []string{"a", "B"}
	got, ok := cleanLines(Merge(base, left, right))
	if !ok {
		t.Fatal("相邻但互不重叠的修改不应产生冲突")
	}
	want := []string{"A", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestIdenticalChangeKeptOnce(t *testing.T) {
	base := []string{"a", "b", "c"}
	left := []string{"a", "X", "b", "c"}
	right := []string{"a", "X", "b", "c"}
	got, ok := cleanLines(Merge(base, left, right))
	if !ok {
		t.Fatal("相同修改不应产生冲突")
	}
	want := []string{"a", "X", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// 两侧同时把同一行改成同样的内容
	base2 := []string{"a", "b", "c"}
	left2 := []string{"a", "Z", "c"}
	right2 := []string{"a", "Z", "c"}
	got2, ok2 := cleanLines(Merge(base2, left2, right2))
	if !ok2 || !reflect.DeepEqual(got2, []string{"a", "Z", "c"}) {
		t.Fatalf("got %v ok=%v", got2, ok2)
	}

	// 两侧同时删除同一区域
	base3 := []string{"a", "b", "c"}
	left3 := []string{"a"}
	right3 := []string{"a"}
	got3, ok3 := cleanLines(Merge(base3, left3, right3))
	if !ok3 || !reflect.DeepEqual(got3, []string{"a"}) {
		t.Fatalf("got %v ok=%v", got3, ok3)
	}
}

func TestConflictBlockAndRanges(t *testing.T) {
	base := []string{"a", "b", "c"}
	left := []string{"a", "L", "c"}
	right := []string{"a", "R", "c"}
	segs := Merge(base, left, right)
	var conflicts []*Conflict
	for _, s := range segs {
		if s.Conflict != nil {
			conflicts = append(conflicts, s.Conflict)
		}
	}
	if len(conflicts) != 1 {
		t.Fatalf("应有 1 个冲突块, got %d", len(conflicts))
	}
	c := conflicts[0]
	if c.BaseLo != 1 || c.BaseHi != 2 || c.LeftLo != 1 || c.LeftHi != 2 || c.RightLo != 1 || c.RightHi != 2 {
		t.Fatalf("三方来源区间错误: %+v", c)
	}
	if !reflect.DeepEqual(c.BaseLines, []string{"b"}) ||
		!reflect.DeepEqual(c.LeftLines, []string{"L"}) ||
		!reflect.DeepEqual(c.RightLines, []string{"R"}) {
		t.Fatalf("冲突内容错误: %+v", c)
	}
}

func TestDuplicateLinesMatchByPosition(t *testing.T) {
	// 重复行按序列位置匹配：删除的是位置上的最后一行，
	// 对首行的修改应作用在位置 0 上。
	base := []string{"x", "x", "x"}
	left := []string{"x", "x"}
	hunks := diff(base, left)
	if len(hunks) != 1 || hunks[0] != (hunk{2, 3, 2, 2}) {
		t.Fatalf("重复行应按位置匹配, got %+v", hunks)
	}
	right := []string{"Y", "x", "x"}
	got, ok := cleanLines(Merge(base, left, right))
	if !ok {
		t.Fatal("不同位置的修改不应冲突")
	}
	want := []string{"Y", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestConflictIsMinimal(t *testing.T) {
	// 两侧在同一位置插入，但共享前缀 P：P 应被削出冲突块。
	base := []string{"a", "b"}
	left := []string{"a", "P", "L", "b"}
	right := []string{"a", "P", "R", "b"}
	segs := Merge(base, left, right)
	var conflicts []*Conflict
	var clean []string
	for _, s := range segs {
		if s.Conflict != nil {
			conflicts = append(conflicts, s.Conflict)
		} else {
			clean = append(clean, s.Lines...)
		}
	}
	if len(conflicts) != 1 {
		t.Fatalf("应有 1 个冲突块, got %d", len(conflicts))
	}
	c := conflicts[0]
	if !reflect.DeepEqual(c.LeftLines, []string{"L"}) || !reflect.DeepEqual(c.RightLines, []string{"R"}) {
		t.Fatalf("冲突块未最小化: %+v", c)
	}
	if !reflect.DeepEqual(clean, []string{"a", "P", "b"}) {
		t.Fatalf("公共前后缀应作为干净输出: %v", clean)
	}
}

func TestSamePointDifferentInsertionConflicts(t *testing.T) {
	base := []string{"a", "b"}
	left := []string{"a", "L", "b"}
	right := []string{"a", "R", "b"}
	_, ok := cleanLines(Merge(base, left, right))
	if ok {
		t.Fatal("同一位置插入不同内容应报冲突，而不是按顺序偷偷偏向一侧")
	}
}

func TestInsertionAdjacentToChangeConflicts(t *testing.T) {
	// 一侧在首行前插入，另一侧修改首行：两者触及同一区域，应报冲突
	// 而不是静默拼接（与 git merge-file 行为一致）。
	base := []string{"a", "b"}
	left := []string{"X", "a", "b"}
	right := []string{"A", "b"}
	_, ok := cleanLines(Merge(base, left, right))
	if ok {
		t.Fatal("插入与相邻修改叠加应报冲突")
	}
}

func TestSharedInsertionNotDuplicated(t *testing.T) {
	// 左侧插入 common；右侧插入 common 并把 gamma 改为 GB。
	// 公共插入应只保留一次，且右侧的额外修改自动合并，不产生冲突。
	base := []string{"gamma"}
	left := []string{"common", "gamma"}
	right := []string{"common", "GB"}
	got, ok := cleanLines(Merge(base, left, right))
	if !ok {
		t.Fatal("公共插入加额外修改应自动合并")
	}
	want := []string{"common", "GB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// enumerate 生成字母表上所有长度 <= maxLen 的序列。
func enumerate(alphabet []string, maxLen int) [][]string {
	var out [][]string
	var rec func(cur []string)
	rec = func(cur []string) {
		cp := make([]string, len(cur))
		copy(cp, cur)
		out = append(out, cp)
		if len(cur) == maxLen {
			return
		}
		for _, a := range alphabet {
			rec(append(cur, a))
		}
	}
	rec(nil)
	return out
}

// mirrored 检查两份输出是否互为左右镜像（交换 left/right 的唯一效果
// 是交换冲突块中的左右内容与区间，其余完全一致）。
func mirrored(a, b []Segment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		sa, sb := a[i], b[i]
		if (sa.Conflict == nil) != (sb.Conflict == nil) {
			return false
		}
		if sa.Conflict == nil {
			if !equalLines(sa.Lines, sb.Lines) {
				return false
			}
			continue
		}
		ca, cb := sa.Conflict, sb.Conflict
		if ca.BaseLo != cb.BaseLo || ca.BaseHi != cb.BaseHi {
			return false
		}
		if ca.LeftLo != cb.RightLo || ca.LeftHi != cb.RightHi ||
			ca.RightLo != cb.LeftLo || ca.RightHi != cb.LeftHi {
			return false
		}
		if !equalLines(ca.BaseLines, cb.BaseLines) ||
			!equalLines(ca.LeftLines, cb.RightLines) ||
			!equalLines(ca.RightLines, cb.LeftLines) {
			return false
		}
	}
	return true
}

// TestNoSideBias 用短序列枚举所有 (base, left, right) 组合，
// 验证合并不以任何方式偏向先处理的一侧。
func TestNoSideBias(t *testing.T) {
	seqs := enumerate([]string{"a", "b"}, 3)
	for _, base := range seqs {
		for _, left := range seqs {
			for _, right := range seqs {
				s1 := Merge(base, left, right)
				s2 := Merge(base, right, left)
				if !mirrored(s1, s2) {
					t.Fatalf("交换两侧后结果不镜像: base=%v left=%v right=%v\n%+v\n%+v",
						base, left, right, s1, s2)
				}
			}
		}
	}
}

// TestEnumerationInvariants 在短序列枚举上验证基本不变量。
func TestEnumerationInvariants(t *testing.T) {
	seqs := enumerate([]string{"a", "b"}, 3)
	for _, base := range seqs {
		for _, left := range seqs {
			for _, right := range seqs {
				segs := Merge(base, left, right)

				// 两侧相同 => 输出即为该侧，且无冲突
				if equalLines(left, right) {
					got, ok := cleanLines(segs)
					if !ok || !equalLines(got, left) {
						t.Fatalf("left==right 时应原样输出: base=%v left=%v", base, left)
					}
					continue
				}
				// 一侧未改 => 输出即为另一侧
				if equalLines(left, base) {
					got, ok := cleanLines(segs)
					if !ok || !equalLines(got, right) {
						t.Fatalf("left==base 时应输出 right: base=%v right=%v", base, right)
					}
					continue
				}
				if equalLines(right, base) {
					got, ok := cleanLines(segs)
					if !ok || !equalLines(got, left) {
						t.Fatalf("right==base 时应输出 left: base=%v left=%v", base, left)
					}
					continue
				}
				// 冲突块互不重叠且按 base 位置严格递增
				prevBaseHi, prevLeftHi, prevRightHi := 0, 0, 0
				first := true
				for _, s := range segs {
					if s.Conflict == nil {
						continue
					}
					c := s.Conflict
					if !first && (c.BaseLo < prevBaseHi || c.LeftLo < prevLeftHi || c.RightLo < prevRightHi) {
						t.Fatalf("冲突块重叠或乱序: base=%v left=%v right=%v", base, left, right)
					}
					if c.BaseLo > c.BaseHi || c.LeftLo > c.LeftHi || c.RightLo > c.RightHi {
						t.Fatalf("区间非法: %+v", c)
					}
					prevBaseHi, prevLeftHi, prevRightHi = c.BaseHi, c.LeftHi, c.RightHi
					first = false
				}
			}
		}
	}
}
