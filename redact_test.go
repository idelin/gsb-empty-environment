package redact

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func writeAll(t *testing.T, r *Redactor, chunks ...string) {
	t.Helper()
	for _, c := range chunks {
		if _, err := r.Write([]byte(c)); err != nil {
			t.Fatalf("Write(%q): %v", c, err)
		}
	}
}

// 词条互为前缀：整块到达时最长匹配胜出。
func TestPrefixEntriesLongestMatch(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "ab", Replacement: "[AB]"},
		Entry{ID: 2, Word: "abc", Replacement: "[ABC]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "abc")
	if got := string(r.Output()); got != "[ABC]" {
		t.Fatalf("output = %q, want %q", got, "[ABC]")
	}
}

// 词条互为前缀：短词条到达时不能提交，因为它仍可能被后文延长。
func TestPrefixEntryHeldPending(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "ab", Replacement: "[AB]"},
		Entry{ID: 2, Word: "abc", Replacement: "[ABC]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "ab")
	if got := string(r.Output()); got != "" {
		t.Fatalf("output after %q = %q, want empty (still pending)", "ab", got)
	}
	if r.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", r.Pending())
	}
	writeAll(t, r, "c")
	if got := string(r.Output()); got != "[ABC]" {
		t.Fatalf("output = %q, want %q", got, "[ABC]")
	}
}

// 词条互为前缀：分多片写入，结果与整块写入一致。
func TestPrefixEntrySplitWrites(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "ab", Replacement: "[AB]"},
		Entry{ID: 2, Word: "abc", Replacement: "[ABC]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "a", "b", "c")
	if got := string(r.Output()); got != "[ABC]" {
		t.Fatalf("output = %q, want %q", got, "[ABC]")
	}
}

// 词条互为前缀：分叉字节证明短词条不可能被延长，立即提交。
func TestPrefixEntryResolvedByDivergingByte(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "ab", Replacement: "[AB]"},
		Entry{ID: 2, Word: "abc", Replacement: "[ABC]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "ab")
	writeAll(t, r, "x")
	if got := string(r.Output()); got != "[AB]x" {
		t.Fatalf("output = %q, want %q", got, "[AB]x")
	}
}

// 相同长度（同一词条重复注册）按词条 ID 决胜，小 ID 胜出。
func TestTieBreakByEntryID(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 9, Word: "key", Replacement: "[NINE]"},
		Entry{ID: 3, Word: "key", Replacement: "[THREE]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "key")
	if got := string(r.Output()); got != "[THREE]" {
		t.Fatalf("output = %q, want %q", got, "[THREE]")
	}
	m := r.Mapping()
	if len(m) != 1 || !m[0].Matched || m[0].EntryID != 3 {
		t.Fatalf("mapping = %+v, want single segment with EntryID 3", m)
	}
}

// 匹配不重叠：左到右贪心，匹配后从其末尾继续扫描。
func TestNonOverlappingMatches(t *testing.T) {
	d := NewDictionary(Entry{ID: 1, Word: "aa", Replacement: "[A]"})
	r := NewRedactor(d)
	writeAll(t, r, "aaa")
	// 末尾的 "a" 仍是 "aa" 的真前缀，流式下保持未决；Flush 后提交。
	if got := string(r.Output()); got != "[A]" {
		t.Fatalf("output before Flush = %q, want %q", got, "[A]")
	}
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := string(r.Output()); got != "[A]a" {
		t.Fatalf("output = %q, want %q", got, "[A]a")
	}
}

// 换版边界：边界之后的字节使用新词典。
func TestVersionChangeAtByteBoundary(t *testing.T) {
	vd := &VersionedDict{}
	vd.Add(1, Entry{ID: 1, Word: "secret", Replacement: "[S]"})
	vd.Remove(2, 1)

	r := NewRedactor(vd.At(1))
	writeAll(t, r, "secret")
	r.SetVersion(vd.At(2))
	writeAll(t, r, "secret")
	if got := string(r.Output()); got != "[S]secret" {
		t.Fatalf("output = %q, want %q", got, "[S]secret")
	}
}

// 换版边界：换版前已写入、仍在未决缓冲中的字节仍受旧词典管辖。
func TestPendingBytesKeepOldVersion(t *testing.T) {
	vd := &VersionedDict{}
	vd.Add(1, Entry{ID: 1, Word: "ab", Replacement: "[AB]"})
	vd.Add(1, Entry{ID: 2, Word: "abc", Replacement: "[ABC]"})
	vd.Remove(2, 2) // v2 removes "abc"

	r := NewRedactor(vd.At(1))
	writeAll(t, r, "ab") // pending under v1: proper prefix of "abc"
	r.SetVersion(vd.At(2))
	writeAll(t, r, "c") // completes "abc", governed by v1 at match start
	if got := string(r.Output()); got != "[ABC]" {
		t.Fatalf("output = %q, want %q", got, "[ABC]")
	}
}

// 换版边界：边界两侧各自使用对应版本的词典做判定。
func TestVersionBoundaryMixedScan(t *testing.T) {
	vd := &VersionedDict{}
	vd.Add(1, Entry{ID: 1, Word: "abc", Replacement: "[ABC]"})
	vd.Add(2, Entry{ID: 2, Word: "ab", Replacement: "[AB]"})
	vd.Remove(2, 1) // v2 只保留 "ab"

	r := NewRedactor(vd.At(1))
	writeAll(t, r, "ab") // pending under v1
	r.SetVersion(vd.At(2))
	writeAll(t, r, "ab") // matches "ab" under v2
	if got := string(r.Output()); got != "ab[AB]" {
		t.Fatalf("output = %q, want %q", got, "ab[AB]")
	}
	m := r.Mapping()
	want := []Segment{
		{InputStart: 0, InputEnd: 2, OutputStart: 0, OutputEnd: 2},
		{InputStart: 2, InputEnd: 4, OutputStart: 2, OutputEnd: 6, EntryID: 2, Matched: true},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("mapping = %+v, want %+v", m, want)
	}
}

// 背压：未决片段超过上限时 Write 拒绝接收（n=0, ErrBackpressure），
// 缓冲保持不变；Flush 强制提交后可以重试。
func TestBackpressureOnOverlongPending(t *testing.T) {
	d := NewDictionary(Entry{ID: 1, Word: "aaaaaaaa", Replacement: "[A]"})
	r := NewRedactor(d, WithMaxPending(4))

	if n, err := r.Write([]byte("aaaa")); err != nil || n != 4 {
		t.Fatalf("first Write = (%d, %v), want (4, nil)", n, err)
	}
	if r.Pending() != 4 {
		t.Fatalf("pending = %d, want 4", r.Pending())
	}

	n, err := r.Write([]byte("aa"))
	if !errors.Is(err, ErrBackpressure) || n != 0 {
		t.Fatalf("second Write = (%d, %v), want (0, ErrBackpressure)", n, err)
	}
	if r.Pending() != 4 {
		t.Fatalf("pending after rejection = %d, want 4 (unchanged)", r.Pending())
	}

	// 补足完整词条后未决片段被消化，不再触发背压。
	if n, err := r.Write([]byte("aaaa")); err != nil || n != 4 {
		t.Fatalf("completing Write = (%d, %v), want (4, nil)", n, err)
	}
	if got := string(r.Output()); got != "[A]" {
		t.Fatalf("output = %q, want %q", got, "[A]")
	}
	if r.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", r.Pending())
	}

	// 再次制造超长未决片段，Flush 强制提交后写可以重试成功。
	writeAll(t, r, "aaaa")
	if _, err := r.Write([]byte("aa")); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("Write after refill: err = %v, want ErrBackpressure", err)
	}
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := string(r.Output()); got != "[A]aaaa" {
		t.Fatalf("output after Flush = %q, want %q", got, "[A]aaaa")
	}
	if n, err := r.Write([]byte("aa")); err != nil || n != 2 {
		t.Fatalf("retry after Flush = (%d, %v), want (2, nil)", n, err)
	}
}

// 来源映射：输出到输入的偏移对应关系，含跨多次提交的合并。
func TestMapping(t *testing.T) {
	d := NewDictionary(Entry{ID: 7, Word: "secret", Replacement: "[S]"})
	r := NewRedactor(d)
	writeAll(t, r, "xx", "sec", "ret yy")
	want := []Segment{
		{InputStart: 0, InputEnd: 2, OutputStart: 0, OutputEnd: 2},
		{InputStart: 2, InputEnd: 8, OutputStart: 2, OutputEnd: 5, EntryID: 7, Matched: true},
		{InputStart: 8, InputEnd: 11, OutputStart: 5, OutputEnd: 8},
	}
	if got := string(r.Output()); got != "xx[S] yy" {
		t.Fatalf("output = %q, want %q", got, "xx[S] yy")
	}
	if !reflect.DeepEqual(r.Mapping(), want) {
		t.Fatalf("mapping = %+v, want %+v", r.Mapping(), want)
	}
}

// 映射覆盖整个输出流：按映射重建输出应与 Output 一致。
func TestMappingCoversOutput(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "token", Replacement: "[T]"},
		Entry{ID: 2, Word: "to", Replacement: "[t]"},
	)
	r := NewRedactor(d)
	writeAll(t, r, "my token to toe tokens")
	if err := r.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	out := r.Output()
	var rebuilt []byte
	var prevOutEnd int64
	for _, s := range r.Mapping() {
		if s.OutputStart != prevOutEnd {
			t.Fatalf("mapping gap: segment %+v starts at %d, previous ended at %d", s, s.OutputStart, prevOutEnd)
		}
		prevOutEnd = s.OutputEnd
		rebuilt = append(rebuilt, out[s.OutputStart:s.OutputEnd]...)
	}
	if string(rebuilt) != string(out) {
		t.Fatalf("rebuilt %q != output %q", rebuilt, out)
	}
	if int(prevOutEnd) != len(out) {
		t.Fatalf("mapping covers %d output bytes, output has %d", prevOutEnd, len(out))
	}
}

// 长流式输入：未决片段被后续字节逐步消化，结果与一次性写入一致。
func TestLongStreamMatchesOneShot(t *testing.T) {
	d := NewDictionary(
		Entry{ID: 1, Word: "password", Replacement: "[P]"},
		Entry{ID: 2, Word: "pass", Replacement: "[p]"},
	)
	input := strings.Repeat("log pass word password passw0rd ", 200)

	oneShot := NewRedactor(d)
	writeAll(t, oneShot, input)
	if err := oneShot.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	streamed := NewRedactor(d)
	for i := 0; i < len(input); i += 7 {
		end := i + 7
		if end > len(input) {
			end = len(input)
		}
		writeAll(t, streamed, input[i:end])
	}
	if err := streamed.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if string(oneShot.Output()) != string(streamed.Output()) {
		t.Fatalf("streamed output differs from one-shot output")
	}
}
