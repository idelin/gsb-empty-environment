package desensitize

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustVersion(t *testing.T, entries ...Entry) *Version {
	t.Helper()
	v, err := NewVersion(entries)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	return v
}

func drainString(m *Masker) string {
	out, _ := m.Drain()
	return string(out)
}

// 词条互为前缀：最长匹配必须胜出，且分块写入不能改变结果。
func TestLongestMatchWithPrefixEntries(t *testing.T) {
	v := mustVersion(t,
		Entry{ID: 1, Pattern: "ab", Replacement: "[AB]"},
		Entry{ID: 2, Pattern: "abcd", Replacement: "[ABCD]"},
	)

	m := NewMasker(v)
	if _, err := m.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	// "ab" 已匹配，但 "abcd" 可能延长它，不能提交。
	if got := drainString(m); got != "" {
		t.Fatalf("premature commit: %q", got)
	}
	if _, err := m.Write([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	// "abcd" 已完整且无法继续延长，可以提交。
	if got := drainString(m); got != "[ABCD]" {
		t.Fatalf("got %q, want [ABCD]", got)
	}

	// 短词条在确定无法延长后胜出。
	out, _, err := Mask(v, []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[AB]c" {
		t.Fatalf("got %q, want [AB]c", out)
	}
}

// 相同词条（同长）按词条 ID 决胜，小 ID 胜出。
func TestTieBreakByEntryID(t *testing.T) {
	v := mustVersion(t,
		Entry{ID: 9, Pattern: "secret", Replacement: "<NINE>"},
		Entry{ID: 3, Pattern: "secret", Replacement: "<THREE>"},
	)
	out, maps, err := Mask(v, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "<THREE>" {
		t.Fatalf("got %q, want <THREE>", out)
	}
	if len(maps) != 1 || maps[0].EntryID != 3 {
		t.Fatalf("mapping = %+v, want winning EntryID 3", maps)
	}
}

// 匹配不重叠：命中后从匹配末尾继续扫描。
func TestNonOverlappingMatches(t *testing.T) {
	v := mustVersion(t, Entry{ID: 1, Pattern: "aa", Replacement: "<A>"})
	out, _, err := Mask(v, []byte("aaaaa"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "<A><A>a" {
		t.Fatalf("got %q, want <A><A>a", out)
	}
}

// 流式提交：只有不可能被后文延长的片段才能提交。
func TestStreamingCommitment(t *testing.T) {
	v := mustVersion(t, Entry{ID: 1, Pattern: "token=", Replacement: "token=<MASK>"})
	m := NewMasker(v)

	steps := []struct {
		in      string
		wantOut string
	}{
		{"tok", ""},             // 仍可能延长，不能提交
		{"en=", "token=<MASK>"}, // 路径已到叶子，不可能再延长，立即提交
		{"x", "x"},              // 不在任何词条路径上，直接提交
	}
	for _, s := range steps {
		if _, err := m.Write([]byte(s.in)); err != nil {
			t.Fatal(err)
		}
		if got := drainString(m); got != s.wantOut {
			t.Fatalf("after %q: got %q, want %q", s.in, got, s.wantOut)
		}
	}
	m.Flush()
	if got := drainString(m); got != "" {
		t.Fatalf("flush: got %q, want empty", got)
	}
}

// 换版从明确字节边界生效：旧版决定存量，新版只管新字节，匹配不跨边界。
func TestVersionSwitchBoundary(t *testing.T) {
	v1 := mustVersion(t, Entry{ID: 1, Pattern: "secret", Replacement: "<S1>"})
	v2 := mustVersion(t, Entry{ID: 2, Pattern: "token", Replacement: "<T2>"})

	m := NewMasker(v1)
	if _, err := m.Write([]byte("sec")); err != nil {
		t.Fatal(err)
	}
	m.SwitchVersion(v2) // "sec" 在 v1 下按输入结束处理：无匹配，提交为字面量
	if _, err := m.Write([]byte("ret token")); err != nil {
		t.Fatal(err)
	}
	m.Flush()
	out, maps := m.Drain()
	// "secret" 被边界劈开不能命中；边界的 "token" 用 v2 命中。
	if string(out) != "secret <T2>" {
		t.Fatalf("got %q, want %q", out, "secret <T2>")
	}
	var sawLiteral, sawV2 bool
	for _, mp := range maps {
		switch mp.EntryID {
		case LiteralEntryID:
			sawLiteral = true
		case 2:
			sawV2 = true
			if mp.EntryID == 1 {
				t.Fatalf("match after switch used v1: %+v", mp)
			}
		}
	}
	if !sawLiteral || !sawV2 {
		t.Fatalf("mappings = %+v, want literal span and v2 match", maps)
	}
}

// 换版后旧词条立即失效。
func TestVersionSwitchRemovesEntries(t *testing.T) {
	v1 := mustVersion(t, Entry{ID: 1, Pattern: "hide", Replacement: "<H>"})
	v2 := mustVersion(t) // 空词典：词条被删
	m := NewMasker(v1)
	if _, err := m.Write([]byte("hide ")); err != nil {
		t.Fatal(err)
	}
	m.SwitchVersion(v2)
	if _, err := m.Write([]byte("hide")); err != nil {
		t.Fatal(err)
	}
	m.Flush()
	if got, _ := m.Drain(); string(got) != "<H> hide" {
		t.Fatalf("got %q, want %q", got, "<H> hide")
	}
}

// 背压：未决片段超长时 Write 报错，Flush 强制提交后恢复。
func TestBackpressureOnLongPending(t *testing.T) {
	long := strings.Repeat("a", 16)
	v := mustVersion(t, Entry{ID: 1, Pattern: long, Replacement: "<LONG>"})
	m := NewMasker(v, WithMaxPending(8))

	// 输入是长词条的前缀，一直无法提交，pending 持续增长。
	n, err := m.Write([]byte(strings.Repeat("a", 10)))
	if n != 10 || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("first write: n=%d err=%v, want n=10 ErrBackpressure", n, err)
	}
	if got := m.Pending(); got != 10 {
		t.Fatalf("pending = %d, want 10", got)
	}
	// 超限期间拒绝新输入。
	if n, err := m.Write([]byte("a")); n != 0 || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("write while over limit: n=%d err=%v", n, err)
	}
	// Flush 强制按当前输入了结：不足 16 字节，全部作为字面量提交。
	m.Flush()
	if got := drainString(m); got != strings.Repeat("a", 10) {
		t.Fatalf("after flush: got %q", got)
	}
	if got := m.Pending(); got != 0 {
		t.Fatalf("pending after flush = %d, want 0", got)
	}
	// 恢复后正常匹配。
	if _, err := m.Write([]byte(long + "!")); err != nil {
		t.Fatal(err)
	}
	m.Flush()
	if got := drainString(m); got != "<LONG>!" {
		t.Fatalf("got %q, want <LONG>!", got)
	}
}

// 来源映射：输出区间能映射回源区间，且跨 Drain 偏移保持绝对。
func TestSourceMapping(t *testing.T) {
	v := mustVersion(t, Entry{ID: 7, Pattern: "secret", Replacement: "***"})
	m := NewMasker(v)
	if _, err := m.Write([]byte("xxsecret yy")); err != nil {
		t.Fatal(err)
	}
	m.Flush()
	out, maps := m.Drain()
	if string(out) != "xx*** yy" {
		t.Fatalf("got %q", out)
	}
	want := []Mapping{
		{OutStart: 0, OutEnd: 2, SrcStart: 0, SrcEnd: 2, EntryID: LiteralEntryID},
		{OutStart: 2, OutEnd: 5, SrcStart: 2, SrcEnd: 8, EntryID: 7},
		{OutStart: 5, OutEnd: 8, SrcStart: 8, SrcEnd: 11, EntryID: LiteralEntryID},
	}
	if !reflect.DeepEqual(maps, want) {
		t.Fatalf("maps = %+v, want %+v", maps, want)
	}

	// 第二段输出的映射偏移应接续第一段。
	if _, err := m.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	m.Flush()
	out2, maps2 := m.Drain()
	if string(out2) != "***" {
		t.Fatalf("got %q", out2)
	}
	want2 := []Mapping{{OutStart: 8, OutEnd: 11, SrcStart: 11, SrcEnd: 17, EntryID: 7}}
	if !reflect.DeepEqual(maps2, want2) {
		t.Fatalf("maps2 = %+v, want %+v", maps2, want2)
	}
}

// 空模式与空词典的边界行为。
func TestEdgeCases(t *testing.T) {
	if _, err := NewVersion([]Entry{{ID: 1, Pattern: ""}}); !errors.Is(err, ErrEmptyPattern) {
		t.Fatalf("err = %v, want ErrEmptyPattern", err)
	}
	empty := mustVersion(t)
	out, maps, err := Mask(empty, []byte("plain"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "plain" {
		t.Fatalf("got %q", out)
	}
	if len(maps) != 1 || maps[0].EntryID != LiteralEntryID {
		t.Fatalf("maps = %+v", maps)
	}
}
