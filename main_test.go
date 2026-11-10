package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

type output struct {
	Conflict int64      `json:"conflict"`
	Gap      int64      `json:"gap"`
	Rows     [][]string `json:"rows"`
}

func runSolve(t *testing.T, in input) output {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := solve(bytes.NewReader(raw), &buf); err != nil {
		t.Fatal(err)
	}
	var got output
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid output JSON %q: %v", buf.String(), err)
	}
	return got
}

func hp(offset int64, content string) part {
	return part{offset: offset, data: []byte(content)}
}

func hpBytes(offset int64, data []byte) part {
	return part{offset: offset, data: data}
}

func TestOutOfOrderNoOverlap(t *testing.T) {
	got := runSolve(t, input{
		Size:  11,
		Parts: []part{hp(6, "world"), hp(0, "hello "), hp(10, "d")},
	})
	if got.Gap != 0 || got.Conflict != 0 || got.Rows == nil {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(got.Rows) != 1 || got.Rows[0][0] != "hello world" {
		t.Fatalf("unexpected rows: %+v", got.Rows)
	}
}

func TestDuplicateRetransmitMerge(t *testing.T) {
	got := runSolve(t, input{
		Size: 11,
		Parts: []part{
			hp(0, "hello"),
			hp(3, "lo wor"),
			hp(6, "world"),
			hp(10, "d"),
		},
	})
	if got.Gap != 0 || got.Conflict != 0 {
		t.Fatalf("expected fully covered, got %+v", got)
	}
	if len(got.Rows) != 1 || got.Rows[0][0] != "hello world" {
		t.Fatalf("unexpected rows: %+v", got.Rows)
	}
}

func TestConflictWinsOverGap(t *testing.T) {
	got := runSolve(t, input{
		Size: 100,
		Parts: []part{
			hp(0, "abcdef"),
			hp(4, "XX"),
		},
	})
	if got.Conflict != 4 {
		t.Fatalf("expected conflict at 4, got %+v", got)
	}
}

func TestConflictSmallestOffset(t *testing.T) {
	got := runSolve(t, input{
		Size: 20,
		Parts: []part{
			hp(0, "abcdef"),
			hp(2, "XX"),
			hp(1, "z"),
		},
	})
	if got.Conflict != 1 {
		t.Fatalf("expected conflict at 1, got %+v", got)
	}
}

func TestConflictAcrossDisjointSegments(t *testing.T) {
	got := runSolve(t, input{
		Size: 20,
		Parts: []part{
			hp(0, "abcd"),
			hp(10, "wxyz"),
			hp(2, "d"),
			hp(12, "x"),
		},
	})
	if got.Conflict != 2 {
		t.Fatalf("expected conflict at 2, got %+v", got)
	}
}

func TestGapFirstMissing(t *testing.T) {
	got := runSolve(t, input{
		Size: 10,
		Parts: []part{
			hp(0, "ab"),
			hp(5, "fgh"),
			hp(3, "de"),
		},
	})
	if got.Gap != 2 {
		t.Fatalf("expected gap at 2, got %+v", got)
	}
}

func TestGapAtEnd(t *testing.T) {
	got := runSolve(t, input{
		Size:  8,
		Parts: []part{hp(0, "abcdef")},
	})
	if got.Gap != 6 {
		t.Fatalf("expected gap at 6, got %+v", got)
	}
}

func TestEmptyFileParsesEmptyRows(t *testing.T) {
	got := runSolve(t, input{Size: 0})
	if len(got.Rows) != 0 {
		t.Fatalf("expected empty rows, got %+v", got)
	}
}

func TestCSVBasicCRLFAndNoTerminator(t *testing.T) {
	csv := "a,b,c\r\n1,2,3\r\nx,y"
	got := runSolve(t, input{
		Size:  int64(len(csv)),
		Parts: []part{hpBytes(0, []byte(csv))},
	})
	want := [][]string{{"a", "b", "c"}, {"1", "2", "3"}, {"x", "y"}}
	if !rowsEqual(got.Rows, want) {
		t.Fatalf("want %v, got %v", want, got.Rows)
	}
}

func TestCSVQuotedCommaAndEscapedQuoteAcrossChunks(t *testing.T) {
	csv := "name,note\r\n\"doe, john\",\"he said \"\"hi\"\"\"\r\nlast,end"
	chunks := splitAt(t, csv, []int{1, 5, 6, 12, 20, 25, len(csv)})
	got := runSolve(t, input{
		Size:  int64(len(csv)),
		Parts: shuffle(chunks),
	})
	want := [][]string{
		{"name", "note"},
		{"doe, john", "he said \"hi\""},
		{"last", "end"},
	}
	if !rowsEqual(got.Rows, want) {
		t.Fatalf("want %v, got %v", want, got.Rows)
	}
}

func TestCSVCRLFInsideQuotesAcrossChunks(t *testing.T) {
	csv := "a,\"line1\r\nline2\",b\r\nx,y,z"
	chunks := splitAt(t, csv, []int{2, 7, 8, 9, 14, len(csv)})
	got := runSolve(t, input{
		Size:  int64(len(csv)),
		Parts: shuffle(chunks),
	})
	want := [][]string{
		{"a", "line1\r\nline2", "b"},
		{"x", "y", "z"},
	}
	if !rowsEqual(got.Rows, want) {
		t.Fatalf("want %v, got %v", want, got.Rows)
	}
}

func TestUTF8RuneSplitAcrossChunks(t *testing.T) {
	csv := "名前,値\r\n\"中文\",你好\r\nemoji,😀end"
	// Split inside the multi-byte sequences deliberately.
	cuts := []int{0, 1, 4, 7, 10, 13, 16, 19, 22, len(csv)}
	chunks := splitAt(t, csv, cuts)
	got := runSolve(t, input{
		Size:  int64(len(csv)),
		Parts: shuffle(chunks),
	})
	want := [][]string{
		{"名前", "値"},
		{"中文", "你好"},
		{"emoji", "😀end"},
	}
	if !rowsEqual(got.Rows, want) {
		t.Fatalf("want %v, got %v", want, got.Rows)
	}
}

func TestRetransmittedOverlappingUTF8Identical(t *testing.T) {
	csv := "a,你好\r\nb,世界"
	first := []byte(csv)
	overlapAt := 4
	got := runSolve(t, input{
		Size: int64(len(first)),
		Parts: []part{
			hpBytes(0, first[:6]),
			hpBytes(int64(overlapAt), first[overlapAt:]),
			hpBytes(0, first),
		},
	})
	want := [][]string{{"a", "你好"}, {"b", "世界"}}
	if !rowsEqual(got.Rows, want) {
		t.Fatalf("want %v, got %v", want, got.Rows)
	}
}

func TestSparseHugeOffsetsMemoryBoundedByReceived(t *testing.T) {
	got := runSolve(t, input{
		Size: 1_000_000_000_000,
		Parts: []part{
			hp(0, "ab"),
			hp(999_999_999_998, "cd"),
		},
	})
	if got.Gap != 2 {
		t.Fatalf("expected gap at 2, got %+v", got)
	}

	stored := segmentSet{}
	stored.insert(999_999_999_990, []byte("0123456789"))
	stored.insert(0, []byte("ab"))
	if len(stored.segs) != 2 {
		t.Fatalf("expected 2 disjoint segments, got %d", len(stored.segs))
	}
	if stored.firstGap(1_000_000_000_000) != 2 {
		t.Fatal("gap check failed on sparse set")
	}
}

func TestAdjacentSegmentsMerge(t *testing.T) {
	stored := segmentSet{}
	stored.insert(0, []byte("abc"))
	stored.insert(3, []byte("def"))
	stored.insert(6, []byte("ghi"))
	if len(stored.segs) != 1 || string(stored.segs[0].data) != "abcdefghi" {
		t.Fatalf("merge failed: %+v", stored.segs)
	}
	if gap := stored.firstGap(9); gap != -1 {
		t.Fatalf("expected full coverage, gap=%d", gap)
	}
}

func TestPartUnmarshalJSON(t *testing.T) {
	var p part
	if err := json.Unmarshal([]byte(`[123,"4142"]`), &p); err != nil {
		t.Fatal(err)
	}
	if p.offset != 123 || string(p.data) != "AB" {
		t.Fatalf("got offset=%d data=%q", p.offset, p.data)
	}
}

func BenchmarkSparseInsert(b *testing.B) {
	const chunk = 1024
	const count = 10_000
	data := bytes.Repeat([]byte("x"), chunk)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		stored := segmentSet{}
		// Disjoint fragments spaced 1 MiB apart: offsets reach ~10 GiB while
		// only ~10 MiB of payload is retained.
		for i := 0; i < count; i++ {
			stored.insert(int64(i)*(1<<20), data)
		}
		if len(stored.segs) != count {
			b.Fatalf("got %d segments", len(stored.segs))
		}
		if gap := stored.firstGap(1 << 40); gap != chunk {
			b.Fatalf("gap=%d", gap)
		}
	}
}

func TestEndToEndViaStdinJSON(t *testing.T) {
	csv := "a,b\r\n\"x,y\",z"
	payload := map[string]any{
		"size": len(csv),
		"parts": [][2]any{
			{0, hex.EncodeToString([]byte(csv[:4]))},
			{4, hex.EncodeToString([]byte(csv[4:]))},
		},
	}
	raw, _ := json.Marshal(payload)
	var buf bytes.Buffer
	if err := solve(bytes.NewReader(raw), &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"rows":[["a","b"],["x,y","z"]]`) {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func splitAt(t *testing.T, s string, cuts []int) []part {
	t.Helper()
	data := []byte(s)
	parts := make([]part, 0, len(cuts))
	prev := 0
	for _, cut := range cuts {
		if cut == prev {
			continue
		}
		parts = append(parts, hpBytes(int64(prev), data[prev:cut]))
		prev = cut
	}
	if prev != len(data) {
		parts = append(parts, hpBytes(int64(prev), data[prev:]))
	}
	return parts
}

func shuffle(parts []part) []part {
	out := make([]part, len(parts))
	for i, p := range parts {
		out[len(parts)-1-i] = p
	}
	return out
}

func rowsEqual(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
