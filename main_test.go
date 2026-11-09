package main

import (
	"bytes"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
)

type part struct {
	offset  int64
	content []byte
}

func feed(t *testing.T, size int64, parts []part) map[string]any {
	t.Helper()
	rawParts := make([][]any, 0, len(parts))
	for _, p := range parts {
		rawParts = append(rawParts, []any{p.offset, hex.EncodeToString(p.content)})
	}
	payload, err := json.Marshal(map[string]any{"size": size, "parts": rawParts})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var out bytes.Buffer
	if err := run(bytes.NewReader(payload), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &result); err != nil {
		t.Fatalf("unmarshal output %q: %v", out.String(), err)
	}
	return result
}

func asRows(t *testing.T, result map[string]any) [][]string {
	t.Helper()
	rawRows, ok := result["rows"].([]any)
	if !ok {
		t.Fatalf("expected rows output, got %v", result)
	}
	rows := make([][]string, 0, len(rawRows))
	for _, rawRow := range rawRows {
		rawFields, ok := rawRow.([]any)
		if !ok {
			t.Fatalf("expected row array, got %v", rawRow)
		}
		fields := make([]string, len(rawFields))
		for i, v := range rawFields {
			fields[i] = v.(string)
		}
		rows = append(rows, fields)
	}
	return rows
}

func TestConflictMinimumOffset(t *testing.T) {
	// Overlap [2,6): c d e f vs c d X f -> first conflict at offset 4.
	result := feed(t, 10, []part{
		{0, []byte("abcdefghij")},
		{2, []byte("cdXf")},
	})
	if got := result["conflict"]; got != float64(4) {
		t.Fatalf("conflict = %v, want 4", got)
	}

	// The smallest conflict offset wins regardless of arrival order.
	result = feed(t, 10, []part{
		{5, []byte("abcde")},      // [5,10): a b c d e
		{0, []byte("01234aXXXX")}, // agrees at 5, differs at 6 onward
		{3, []byte("ZZZZ")},       // differs at 3 inside [3,7)
	})
	if got := result["conflict"]; got != float64(3) {
		t.Fatalf("conflict = %v, want 3", got)
	}
}

func TestGapFirstMissingOffset(t *testing.T) {
	result := feed(t, 10, []part{
		{0, []byte("ab")},   // [0,2)
		{4, []byte("ef")},   // [4,6)
		{6, []byte("ghij")}, // [6,10)
	})
	if got := result["gap"]; got != float64(2) {
		t.Fatalf("gap = %v, want 2", got)
	}

	result = feed(t, 10, []part{
		{1, []byte("bcdefghij")}, // missing offset 0
	})
	if got := result["gap"]; got != float64(0) {
		t.Fatalf("gap = %v, want 0", got)
	}

	result = feed(t, 10, []part{
		{0, []byte("abcdefgh")}, // missing the last two bytes
	})
	if got := result["gap"]; got != float64(8) {
		t.Fatalf("gap = %v, want 8", got)
	}
}

func TestOutOfOrderAndOverlapAssembly(t *testing.T) {
	csvBytes := []byte("a,b,c\r\n\"x,y\",z\r\nlast")
	size := int64(len(csvBytes))

	parts := []part{
		{9, csvBytes[9:]},
		{0, csvBytes[:7]},
		{5, csvBytes[5:12]},
		{11, csvBytes[11:]},
		{3, csvBytes[3:10]},
	}
	rand.New(rand.NewSource(42)).Shuffle(len(parts), func(i, j int) {
		parts[i], parts[j] = parts[j], parts[i]
	})
	result := feed(t, size, parts)
	want := [][]string{
		{"a", "b", "c"},
		{"x,y", "z"},
		{"last"},
	}
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestQuotedQuoteSplitAcrossParts(t *testing.T) {
	// Field value: he said "hi" -> RFC 4180 encoding "he said ""hi"""
	csvBytes := []byte("name\r\n\"he said \"\"hi\"\"\"")
	size := int64(len(csvBytes))

	// Cut between the two escape-quote characters.
	cut := bytes.Index(csvBytes, []byte(`""`)) + 1
	result := feed(t, size, []part{
		{int64(cut), csvBytes[cut:]},
		{0, csvBytes[:cut]},
	})
	want := [][]string{{"name"}, {`he said "hi"`}}
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}

	// Three-way split with an overlapping copy around the escaped quote.
	result = feed(t, size, []part{
		{0, csvBytes[:cut+1]},
		{int64(cut - 1), csvBytes[cut-1 : cut+2]},
		{int64(cut + 1), csvBytes[cut+1:]},
	})
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestCRLFSplitInsideQuotedField(t *testing.T) {
	csvBytes := []byte("\"line1\r\nline2\",tail\r\nend")
	size := int64(len(csvBytes))
	cr := bytes.IndexByte(csvBytes, '\r')

	// CR and LF arrive in different fragments; they stay inside the field.
	result := feed(t, size, []part{
		{int64(cr + 1), csvBytes[cr+1:]},
		{0, csvBytes[:cr+1]},
	})
	want := [][]string{
		{"line1\r\nline2", "tail"},
		{"end"},
	}
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestUTF8SplitAcrossParts(t *testing.T) {
	// 中 = E4 B8 AD and 🚀 = F0 9F 9A 80, both split across fragment edges.
	csvBytes := []byte("中,文 \"emoji\" 🚀\r\n\"你好\",世界")
	size := int64(len(csvBytes))

	// Fragments tile [0, size) with cuts inside multi-byte sequences and
	// one overlap across the emoji boundary.
	chunks := []part{
		{0, csvBytes[:2]},     // first two bytes of 中
		{2, csvBytes[2:5]},    // last byte of 中 through start of 文
		{4, csvBytes[4:18]},   // overlap at 4, cuts through 🚀 at bytes 16-17
		{17, csvBytes[17:25]}, // rest of 🚀, CRLF, opening quote, start of 你
		{24, csvBytes[24:]},   // overlap at 24 through the final 世界
	}
	result := feed(t, size, chunks)
	want := [][]string{
		{"中", `文 "emoji" 🚀`},
		{"你好", "世界"},
	}
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}

	// A disagreeing byte inside a multi-byte sequence reports its byte offset.
	bad := append([]byte(nil), csvBytes...)
	bad[3] ^= 0xFF
	result = feed(t, size, []part{
		{0, csvBytes},
		{3, bad[3:5]},
	})
	if got := result["conflict"]; got != float64(3) {
		t.Fatalf("conflict = %v, want 3", got)
	}
}

func TestEmptyAndSingleRecord(t *testing.T) {
	result := feed(t, 0, nil)
	if got := asRows(t, result); len(got) != 0 {
		t.Fatalf("empty file rows = %q, want []", got)
	}

	result = feed(t, 1, []part{{0, []byte("x")}})
	if got := asRows(t, result); !reflect.DeepEqual(got, [][]string{{"x"}}) {
		t.Fatalf("single record = %q", got)
	}

	crlf := []byte("\r\n")
	result = feed(t, int64(len(crlf)), []part{{0, crlf}})
	if got := asRows(t, result); !reflect.DeepEqual(got, [][]string{{""}}) {
		t.Fatalf("blank line = %q", got)
	}

	// Empty quoted field, multiple records, and a quoted field ending the file.
	csvBytes := []byte("\"\",x\r\n\r\n\"quoted\"")
	result = feed(t, int64(len(csvBytes)), []part{{0, csvBytes}})
	want := [][]string{{"", "x"}, {""}, {"quoted"}}
	if got := asRows(t, result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestRandomSparseReassembly(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		base := make([]byte, 1+rng.Intn(500))
		rand.New(rand.NewSource(int64(iter*31 + 1))).Read(base)

		var parts []part
		pos := 0
		for pos < len(base) {
			span := 1 + rng.Intn(20)
			start := pos
			if start > 0 && rng.Intn(2) == 0 {
				start -= rng.Intn(start)
			}
			end := pos + span
			if end > len(base) {
				end = len(base)
			}
			parts = append(parts, part{int64(start), append([]byte(nil), base[start:end]...)})
			pos = end
			if pos < len(base) && rng.Intn(4) == 0 {
				pos--
			}
		}
		rng.Shuffle(len(parts), func(i, j int) {
			parts[i], parts[j] = parts[j], parts[i]
		})

		result := feed(t, int64(len(base)), parts)
		if _, ok := result["conflict"]; ok {
			t.Fatalf("iter %d: unexpected conflict", iter)
		}
		if _, ok := result["gap"]; ok {
			t.Fatalf("iter %d: unexpected gap", iter)
		}

		if looksLikeCSVText(base) {
			got := asRows(t, result)
			want, err := readStdCSV(base)
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("iter %d: rows %q != %q", iter, got, want)
			}
		}
	}
}

func looksLikeCSVText(b []byte) bool {
	inQuotes := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '"' {
			if inQuotes && i+1 < len(b) && b[i+1] == '"' {
				i++
				continue
			}
			inQuotes = !inQuotes
			continue
		}
		if c == '\n' || c == '\r' {
			if !inQuotes && (c != '\r' || i+1 >= len(b) || b[i+1] != '\n') {
				return false
			}
		}
		if c < 0x20 && c != '\r' && c != '\n' {
			return false
		}
	}
	return !inQuotes
}

func readStdCSV(b []byte) ([][]string, error) {
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	rows := make([][]string, 0)
	for {
		rec, err := r.Read()
		if err != nil {
			return rows, err
		}
		rows = append(rows, rec)
	}
}

func TestStoreIntervalInvariants(t *testing.T) {
	const huge = int64(1_000_000_000_000)
	st := newStore()
	st.insert(huge-3, []byte("abc"))
	st.insert(10, []byte("xy"))
	st.insert(12, []byte("z!")) // adjacent to [10,12) -> [10,13)
	st.insert(11, []byte("yz")) // agreeing overlap

	if gap := st.firstGap(huge); gap != 0 {
		t.Fatalf("gap = %d, want 0", gap)
	}

	// Large offsets use 64-bit arithmetic and sparse storage: a few 64 MiB
	// intervals separated by gaps, never materializing the 1e12-byte span.
	const chunk = int64(64 << 20)
	for _, off := range []int64{chunk * 3, chunk * 10, huge - chunk} {
		st.insert(off, bytes.Repeat([]byte{byte(off >> 24)}, int(chunk)))
	}
	if gap := st.firstGap(huge); gap != 0 {
		t.Fatalf("gap = %d, want 0 (offset 0 still missing)", gap)
	}

	var prevEnd int64
	var check func(n *node)
	check = func(n *node) {
		if n == nil {
			return
		}
		check(n.left)
		if n.start < prevEnd {
			t.Fatalf("overlapping intervals remain in treap")
		}
		prevEnd = n.end
		wantMax := n.end
		if e := maxEndOf(n.left); e > wantMax {
			wantMax = e
		}
		if e := maxEndOf(n.right); e > wantMax {
			wantMax = e
		}
		if n.maxEnd != wantMax {
			t.Fatalf("maxEnd = %d, want %d", n.maxEnd, wantMax)
		}
		check(n.right)
	}
	check(st.root)

	// Dense case on a small range verifies full coverage and flatten output.
	small := newStore()
	payload := []byte("0123456789ABCDEF")
	small.insert(0, payload[:6])
	small.insert(5, payload[5:12]) // overlap, agreeing bytes
	small.insert(12, payload[12:]) // adjacency
	small.insert(16, []byte{})     // empty fragment is ignored
	if gap := small.firstGap(int64(len(payload))); gap != -1 {
		t.Fatalf("small gap = %d, want -1", gap)
	}
	if got := flatten(small.root, nil); !bytes.Equal(got, payload) {
		t.Fatalf("flatten = %q, want %q", got, payload)
	}
}
