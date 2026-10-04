package rollsam

import (
	"math/rand"
	"reflect"
	"testing"
)

// ---------- 参考实现：暴力枚举 ----------

func refDistinct(s string) int64 {
	set := map[string]struct{}{}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j <= len(s); j++ {
			set[s[i:j]] = struct{}{}
		}
	}
	return int64(len(set))
}

func refPositions(s, pat string) []int {
	if pat == "" {
		out := make([]int, len(s)+1)
		for i := range out {
			out[i] = i
		}
		return out
	}
	var out []int
	for i := 0; i+len(pat) <= len(s); i++ {
		if s[i:i+len(pat)] == pat {
			out = append(out, i)
		}
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkAgainstRef 用暴力参考全面校验：不同子串数 + 全部子串位置 + 空模式 + 不出现的模式。
func checkAgainstRef(t *testing.T, s *SAM, text string) {
	t.Helper()
	if s.Len() != len(text) {
		t.Fatalf("Len: got %d, want %d", s.Len(), len(text))
	}
	if got, want := s.Distinct(), refDistinct(text); got != want {
		t.Fatalf("Distinct(%q): got %d, want %d", text, got, want)
	}
	seen := map[string]struct{}{}
	for i := 0; i < len(text); i++ {
		for j := i + 1; j <= len(text); j++ {
			pat := text[i:j]
			if _, ok := seen[pat]; ok {
				continue
			}
			seen[pat] = struct{}{}
			if got, want := s.Positions(pat), refPositions(text, pat); !equalInts(got, want) {
				t.Fatalf("Positions(%q in %q): got %v, want %v", pat, text, got, want)
			}
		}
	}
	if got, want := s.Positions(""), refPositions(text, ""); !equalInts(got, want) {
		t.Fatalf("Positions(empty in %q): got %v, want %v", text, got, want)
	}
	for _, pat := range []string{"\x00", "\x01\x02", text + "\x00"} {
		if got := s.Positions(pat); got != nil {
			t.Fatalf("Positions(%q in %q): got %v, want nil", pat, text, got)
		}
	}
}

func build(text string) *SAM {
	s := New()
	for i := 0; i < len(text); i++ {
		s.Append(text[i])
	}
	return s
}

// ---------- 测试 ----------

// 枚举所有长度 <= 8 的二进制串，逐步校验。
func TestExhaustiveSmallBinary(t *testing.T) {
	var rec func(cur string, depth int)
	rec = func(cur string, depth int) {
		s := build(cur)
		checkAgainstRef(t, s, cur)
		if depth == 0 {
			return
		}
		rec(cur+"a", depth-1)
		rec(cur+"b", depth-1)
	}
	rec("", 8)
}

func TestRepeatedChars(t *testing.T) {
	text := "aaaaaaaa"
	s := build(text)
	checkAgainstRef(t, s, text)
	// "aaa" 在 "aaaaaaaa" 中起始于 0..5
	if got, want := s.Positions("aaa"), []int{0, 1, 2, 3, 4, 5}; !equalInts(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// n 个相同字符的不同子串只有 n 个
	if got := s.Distinct(); got != 8 {
		t.Fatalf("Distinct: got %d, want 8", got)
	}
}

func TestAlternating(t *testing.T) {
	for _, text := range []string{"abababab", "babababa", "abcabcabc"} {
		checkAgainstRef(t, build(text), text)
	}
}

// 撤销后改接另一后缀，结构必须与从头构建完全一致（无残留转移、无重复计数）。
func TestUndoThenOtherSuffix(t *testing.T) {
	s := build("ababa")
	if !s.Undo() || !s.Undo() {
		t.Fatal("Undo failed")
	}
	checkAgainstRef(t, s, "aba")
	s.Append('b')
	s.Append('b')
	checkAgainstRef(t, s, "ababb")

	fresh := build("ababb")
	if !reflect.DeepEqual(s.nodes, fresh.nodes) {
		t.Fatal("residual transitions: structure differs from fresh build")
	}
	if s.last != fresh.last || s.distinct != fresh.distinct || s.n != fresh.n {
		t.Fatal("scalar state differs from fresh build")
	}
}

// 全部撤销后应回到与 New 相同的状态，且可继续追加。
func TestUndoAll(t *testing.T) {
	s := build("abca")
	for s.Undo() {
	}
	if s.Len() != 0 || s.Distinct() != 0 {
		t.Fatalf("not empty after full undo: n=%d distinct=%d", s.Len(), s.Distinct())
	}
	if got := s.Positions(""); !equalInts(got, []int{0}) {
		t.Fatalf("empty pattern on empty text: got %v, want [0]", got)
	}
	if got := s.Positions("a"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if !reflect.DeepEqual(s.nodes, New().nodes) {
		t.Fatal("nodes differ from fresh automaton after full undo")
	}
	s.Append('z')
	checkAgainstRef(t, s, "z")
}

func TestUndoEmpty(t *testing.T) {
	if New().Undo() {
		t.Fatal("Undo on empty history should return false")
	}
}

// 随机追加/撤销混合操作，每步与暴力参考对比，定期与从头构建对比结构。
func TestRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := New()
	text := ""
	for op := 0; op < 400; op++ {
		if len(text) == 0 || rng.Intn(10) < 6 {
			c := byte('a' + rng.Intn(3))
			s.Append(c)
			text += string(c)
		} else {
			s.Undo()
			text = text[:len(text)-1]
		}
		if got, want := s.Distinct(), refDistinct(text); got != want {
			t.Fatalf("op %d, text %q: Distinct got %d, want %d", op, text, got, want)
		}
		if op%20 == 0 {
			checkAgainstRef(t, s, text)
			if fresh := build(text); !reflect.DeepEqual(s.nodes, fresh.nodes) {
				t.Fatalf("op %d, text %q: structure differs from fresh build", op, text)
			}
		}
	}
}

// 撤销的时序：交错追加与撤销，验证计数不重复、不遗漏。
func TestInterleavedUndoCounts(t *testing.T) {
	s := New()
	text := ""
	push := func(c byte) { s.Append(c); text += string(c) }
	pop := func() { s.Undo(); text = text[:len(text)-1] }
	push('a')
	push('b')
	push('a')
	push('b') // "abab"
	pop()     // "aba"
	push('b') // "abab"
	push('a') // "ababa"
	pop()     // "abab"
	pop()     // "aba"
	push('a') // "abaa"
	checkAgainstRef(t, s, "abaa")
}
