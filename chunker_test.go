package chunker

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

// dripReader yields the input one byte at a time so that multi-byte
// characters always straddle read boundaries.
type dripReader struct{ b []byte }

func (d *dripReader) Read(p []byte) (int, error) {
	if len(d.b) == 0 {
		return 0, io.EOF
	}
	p[0] = d.b[0]
	d.b = d.b[1:]
	return 1, nil
}

type wantChunk struct {
	data  string
	start int64
	end   int64
	reps  []int64 // original offsets of replaced invalid bytes
}

type wantErr struct {
	offset int64
	ru     rune
	size   int
}

var cases = []struct {
	name   string
	in     string
	budget int
	chunks []wantChunk
	errs   []wantErr
}{
	{
		name:   "empty",
		in:     "",
		budget: 4,
	},
	{
		name:   "ascii exact fit",
		in:     "abcdefgh",
		budget: 4,
		chunks: []wantChunk{
			{"abcd", 0, 4, nil},
			{"efgh", 4, 8, nil},
		},
	},
	{
		name:   "ascii remainder",
		in:     "abcde",
		budget: 2,
		chunks: []wantChunk{
			{"ab", 0, 2, nil},
			{"cd", 2, 4, nil},
			{"e", 4, 5, nil},
		},
	},
	{
		name:   "multibyte straddles budget boundary",
		in:     "ab中cd", // 中 = 3 bytes at offset 2
		budget: 4,
		chunks: []wantChunk{
			{"ab", 0, 2, nil}, // 中 does not fit; moves whole, never split
			{"中c", 2, 6, nil},
			{"d", 6, 7, nil},
		},
	},
	{
		name:   "all widths",
		in:     "aé中\U0001F600", // 1+2+3+4 bytes
		budget: 5,
		chunks: []wantChunk{
			{"aé", 0, 3, nil},
			{"中", 3, 6, nil},
			{"\U0001F600", 6, 10, nil},
		},
	},
	{
		name:   "budget equals rune size",
		in:     "中中",
		budget: 3,
		chunks: []wantChunk{
			{"中", 0, 3, nil},
			{"中", 3, 6, nil},
		},
	},
	{
		name:   "single char exceeds budget",
		in:     "a中b",
		budget: 2,
		chunks: []wantChunk{
			{"a", 0, 1, nil},
			{"b", 4, 5, nil},
		},
		errs: []wantErr{{1, '中', 3}},
	},
	{
		name:   "only oversized chars",
		in:     "中中",
		budget: 2,
		errs: []wantErr{
			{0, '中', 3},
			{3, '中', 3},
		},
	},
	{
		name:   "invalid bytes become replacement chars",
		in:     "a\xffb\x80c",
		budget: 8,
		chunks: []wantChunk{
			{"a�b�", 0, 4, []int64{1, 3}}, // 8 output bytes, budget full
			{"c", 4, 5, nil},
		},
	},
	{
		name:   "truncated sequence at EOF",
		in:     "ok\xc3", // lone lead byte at end of stream
		budget: 8,
		chunks: []wantChunk{
			{"ok�", 0, 3, []int64{2}},
		},
	},
	{
		name:   "truncated sequence mid stream",
		in:     "\xe4\xb8x", // 中 missing its last byte
		budget: 8,
		chunks: []wantChunk{
			{"��x", 0, 3, []int64{0, 1}},
		},
	},
	{
		name:   "overlong encoding",
		in:     "\xc0\xaf", // overlong '/'
		budget: 8,
		chunks: []wantChunk{
			{"��", 0, 2, []int64{0, 1}},
		},
	},
	{
		name:   "replacement char crosses budget boundary",
		in:     "ab\xff", // replacement is 3 bytes, does not fit after "ab"
		budget: 4,
		chunks: []wantChunk{
			{"ab", 0, 2, nil},
			{"�", 2, 3, []int64{2}},
		},
	},
	{
		name:   "replacement exceeds tiny budget",
		in:     "\xffa",
		budget: 2,
		chunks: []wantChunk{
			{"a", 1, 2, nil},
		},
		errs: []wantErr{{0, utf8.RuneError, 3}},
	},
	{
		name:   "mixed valid invalid and oversized",
		in:     "x\xfe中y",
		budget: 3,
		chunks: []wantChunk{
			{"x", 0, 1, nil},
			{"�", 1, 2, []int64{1}},
			{"中", 2, 5, nil},
			{"y", 5, 6, nil},
		},
	},
	{
		name:   "budget one",
		in:     "abc",
		budget: 1,
		chunks: []wantChunk{
			{"a", 0, 1, nil},
			{"b", 1, 2, nil},
			{"c", 2, 3, nil},
		},
	},
}

func check(t *testing.T, name string, chunks []Chunk, errs []RuneError, err error, budget int, want []wantChunk, wantErrs []wantErr) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", name, err)
	}
	if len(chunks) != len(want) {
		t.Fatalf("%s: got %d chunks %q, want %d", name, len(chunks), chunkStrings(chunks), len(want))
	}
	for i, c := range chunks {
		w := want[i]
		if string(c.Data) != w.data {
			t.Errorf("%s: chunk %d data = %q, want %q", name, i, c.Data, w.data)
		}
		if c.Start != w.start || c.End != w.end {
			t.Errorf("%s: chunk %d source = [%d,%d), want [%d,%d)", name, i, c.Start, c.End, w.start, w.end)
		}
		if len(c.Data) > budget {
			t.Errorf("%s: chunk %d is %d bytes, over budget %d", name, i, len(c.Data), budget)
		}
		if !utf8.Valid(c.Data) {
			t.Errorf("%s: chunk %d is not valid UTF-8: %q", name, i, c.Data)
		}
		var reps []int64
		for _, r := range c.Replacements {
			reps = append(reps, r.Offset)
			if got := string(c.Data[r.At : r.At+3]); got != "�" {
				t.Errorf("%s: chunk %d replacement at %d is %q, want U+FFFD", name, i, r.At, got)
			}
		}
		if !eqInt64s(reps, w.reps) {
			t.Errorf("%s: chunk %d replacement offsets = %v, want %v", name, i, reps, w.reps)
		}
	}
	// Source ranges must tile the consumed input with no gaps or overlaps.
	var prev int64
	for i, c := range chunks {
		if i > 0 && c.Start < prev {
			t.Errorf("%s: chunk %d starts at %d before previous end %d", name, i, c.Start, prev)
		}
		prev = c.End
	}
	if len(errs) != len(wantErrs) {
		t.Fatalf("%s: got %d rune errors %v, want %d", name, len(errs), errs, len(wantErrs))
	}
	for i, e := range errs {
		w := wantErrs[i]
		if e.Offset != w.offset || e.Rune != w.ru || e.Size != w.size {
			t.Errorf("%s: error %d = %+v, want offset=%d rune=%#U size=%d", name, i, e, w.offset, w.ru, w.size)
		}
	}
}

func TestSplit(t *testing.T) {
	for _, tc := range cases {
		chunks, errs, err := Split(strings.NewReader(tc.in), tc.budget)
		check(t, tc.name, chunks, errs, err, tc.budget, tc.chunks, tc.errs)
	}
}

// TestSplitDrip feeds the same cases one byte per Read so multi-byte
// characters straddle read boundaries; results must be identical.
func TestSplitDrip(t *testing.T) {
	for _, tc := range cases {
		chunks, errs, err := Split(&dripReader{b: []byte(tc.in)}, tc.budget)
		check(t, tc.name+" (drip)", chunks, errs, err, tc.budget, tc.chunks, tc.errs)
	}
}

func TestSplitBadBudget(t *testing.T) {
	if _, _, err := Split(strings.NewReader("x"), 0); err == nil {
		t.Fatal("expected error for budget 0")
	}
}

func chunkStrings(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = string(c.Data)
	}
	return out
}

func eqInt64s(a, b []int64) bool {
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
