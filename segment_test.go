package sstdict

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// buildSegment is a helper that builds a segment from sorted unique words.
func buildSegment(t *testing.T, words []string, blockSize int) *Segment {
	t.Helper()
	b := NewBuilder(blockSize)
	for _, w := range words {
		if err := b.Add(w); err != nil {
			t.Fatalf("Add(%q): %v", w, err)
		}
	}
	seg, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return seg
}

// randomWords generates n sorted unique words with shared prefixes,
// including UTF-8 multibyte strings.
func randomWords(r *rand.Rand, n int) []string {
	set := make(map[string]struct{})
	stems := []string{"app", "ban", "中文词", "pré", "\xe4\xb8\xad", "x", "日本語の"}
	for len(set) < n {
		stem := stems[r.Intn(len(stems))]
		w := stem + string(rune('a'+r.Intn(26))) + string(rune('0'+r.Intn(10))) + strings.Repeat("z", r.Intn(4))
		set[w] = struct{}{}
	}
	out := make([]string, 0, n)
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// TestGetAgainstReferenceArray checks random access against an independent
// []string for every ordinal, and that a single Get never decodes more
// than blockSize entries.
func TestGetAgainstReferenceArray(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	words := randomWords(r, 5000)
	const blockSize = 16
	seg := buildSegment(t, words, blockSize)
	var st Stats
	for k := range words {
		got, err := seg.Get(uint64(k), &st)
		if err != nil {
			t.Fatalf("Get(%d): %v", k, err)
		}
		if got != words[k] {
			t.Fatalf("Get(%d) = %q, want %q", k, got, words[k])
		}
	}
	if st.RandomReads != uint64(len(words)) {
		t.Fatalf("RandomReads = %d, want %d", st.RandomReads, len(words))
	}
	if st.MaxDecodedPerRead > blockSize {
		t.Fatalf("MaxDecodedPerRead = %d exceeds block size %d", st.MaxDecodedPerRead, blockSize)
	}
	t.Logf("random reads: %d, max entries decoded per read: %d (block size %d)",
		st.RandomReads, st.MaxDecodedPerRead, blockSize)
	if _, err := seg.Get(uint64(len(words)), nil); !errors.Is(err, ErrRange) {
		t.Fatalf("Get out of range: %v, want ErrRange", err)
	}
}

// TestPrefixRangeAgainstReferenceArray checks byte-prefix range queries
// against naive filtering of an independent []string, including prefixes
// that cut a UTF-8 rune in half. Outputs must be complete valid strings.
func TestPrefixRangeAgainstReferenceArray(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	words := randomWords(r, 3000)
	seg := buildSegment(t, words, 8)
	prefixes := []string{"", "a", "app", "中", "\xe4\xb8", "日本", "zzz-no-match"}
	for i := 0; i < 200; i++ {
		w := words[r.Intn(len(words))]
		prefixes = append(prefixes, w[:r.Intn(len(w)+1)]) // byte prefix, may split a rune
	}
	var st Stats
	for _, p := range prefixes {
		got, err := seg.PrefixRange(p, &st)
		if err != nil {
			t.Fatalf("PrefixRange(%q): %v", p, err)
		}
		var want []string
		for _, w := range words {
			if strings.HasPrefix(w, p) {
				want = append(want, w)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("PrefixRange(%q): got %d words, want %d", p, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("PrefixRange(%q)[%d] = %q, want %q", p, i, got[i], want[i])
			}
			if !utf8.ValidString(got[i]) {
				t.Fatalf("PrefixRange(%q)[%d] = %q is not valid UTF-8", p, i, got[i])
			}
		}
	}
	t.Logf("prefix scans: %d, blocks decoded: %d, entries decoded: %d",
		st.PrefixScans, st.ScanBlocksDecoded, st.ScanEntries)
}

// TestEmptyFirstWord ensures an empty string can be the first entry.
func TestEmptyFirstWord(t *testing.T) {
	words := []string{"", "a", "ab", "abc"}
	seg := buildSegment(t, words, 2)
	for k, want := range words {
		got, err := seg.Get(uint64(k), nil)
		if err != nil || got != want {
			t.Fatalf("Get(%d) = %q, %v; want %q", k, got, err, want)
		}
	}
	all, err := seg.PrefixRange("", nil)
	if err != nil || len(all) != len(words) {
		t.Fatalf("PrefixRange(\"\") = %v, %v", all, err)
	}
}

// TestDuplicateAndUnsortedPolicy documents the batch duplicate policy:
// duplicates and out-of-order input are rejected, never silently stored.
func TestDuplicateAndUnsortedPolicy(t *testing.T) {
	b := NewBuilder(4)
	if err := b.Add("a"); err != nil {
		t.Fatal(err)
	}
	if err := b.Add("a"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate Add: %v, want ErrDuplicate", err)
	}
	if err := b.Add("0"); !errors.Is(err, ErrUnsorted) {
		t.Fatalf("unsorted Add: %v, want ErrUnsorted", err)
	}
}

// TestCorruptBlockDetected flips a payload byte and requires the read to
// fail loudly (CRC), never silently mis-decode.
func TestCorruptBlockDetected(t *testing.T) {
	words := randomWords(rand.New(rand.NewSource(1)), 500)
	seg := buildSegment(t, words, 16)
	// Corrupt a byte inside the second block's payload.
	second := seg.index[1].offset
	abs := headerSize + int(second)
	payloadLen := int(binary.LittleEndian.Uint32(seg.data[abs:]))
	victim := abs + 8 + payloadLen/2
	seg.data[victim] ^= 0xFF
	// A read landing in that block must fail.
	_, err := seg.Get(16, nil)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get into corrupt block: %v, want ErrCorrupt", err)
	}
	// A read in an intact block must still succeed.
	got, err := seg.Get(0, nil)
	if err != nil || got != words[0] {
		t.Fatalf("Get(0) on intact block = %q, %v; want %q", got, err, words[0])
	}
}

// craftBlock builds a raw block payload from explicit entries and returns
// the full segment buffer, so tests can inject structurally invalid but
// CRC-valid blocks.
func craftSegment(t *testing.T, blockSize int, numWords uint64, payload []byte, first string) []byte {
	t.Helper()
	var block []byte
	block = binary.LittleEndian.AppendUint32(block, uint32(len(payload)))
	block = binary.LittleEndian.AppendUint32(block, crc32.ChecksumIEEE(payload))
	block = append(block, payload...)
	var index []byte
	index = appendUvarint(index, uint64(len(first)))
	index = append(index, first...)
	index = appendUvarint(index, 0)
	var hdr [headerSize]byte
	copy(hdr[0:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(blockSize))
	binary.LittleEndian.PutUint32(hdr[8:12], 1)
	binary.LittleEndian.PutUint64(hdr[12:20], numWords)
	binary.LittleEndian.PutUint32(hdr[20:24], uint32(headerSize+len(block)))
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(len(index)))
	out := append(hdr[:], block...)
	return append(out, index...)
}

// TestCorruptPrefixLengthRejected proves a corrupt prefixLen is rejected
// before allocation and cannot reach outside its own block.
func TestCorruptPrefixLengthRejected(t *testing.T) {
	// Block: restart word "ab", then an entry claiming prefixLen=2^40.
	payload := appendUvarint(nil, 2)
	payload = appendUvarint(payload, 2)
	payload = append(payload, "ab"...)
	payload = appendUvarint(payload, 1<<40) // corrupt prefixLen
	payload = appendUvarint(payload, 1)
	payload = append(payload, 'x')
	buf := craftSegment(t, 4, 2, payload, "ab")
	seg, err := OpenSegment(buf)
	if err != nil {
		t.Fatalf("OpenSegment: %v", err)
	}
	_, err = seg.Get(1, nil)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get with corrupt prefixLen: %v, want ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "prefix length") {
		t.Fatalf("error should name the prefix length: %v", err)
	}
}

// TestCorruptSuffixLengthRejected proves a corrupt suffixLen is rejected
// against the remaining payload before allocation.
func TestCorruptSuffixLengthRejected(t *testing.T) {
	payload := appendUvarint(nil, 2)
	payload = appendUvarint(payload, 2)
	payload = append(payload, "ab"...)
	payload = appendUvarint(payload, 1)
	payload = appendUvarint(payload, 1<<40) // corrupt suffixLen
	buf := craftSegment(t, 4, 2, payload, "ab")
	seg, err := OpenSegment(buf)
	if err != nil {
		t.Fatalf("OpenSegment: %v", err)
	}
	if _, err := seg.Get(1, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get with corrupt suffixLen: %v, want ErrCorrupt", err)
	}
}

// TestCorruptEntryCountRejected proves an entry count above blockSize is
// rejected before the words slice is allocated.
func TestCorruptEntryCountRejected(t *testing.T) {
	payload := appendUvarint(nil, 1<<40) // corrupt entry count
	payload = appendUvarint(payload, 1)
	payload = append(payload, 'a')
	buf := craftSegment(t, 4, 1, payload, "a")
	seg, err := OpenSegment(buf)
	if err != nil {
		t.Fatalf("OpenSegment: %v", err)
	}
	if _, err := seg.Get(0, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get with corrupt entry count: %v, want ErrCorrupt", err)
	}
}

// TestBlocksAreSelfContained decodes every block independently and confirms no
// block needs content from a previous block to rebuild its words.
func TestBlocksAreSelfContained(t *testing.T) {
	words := randomWords(rand.New(rand.NewSource(3)), 1000)
	const blockSize = 8
	seg := buildSegment(t, words, blockSize)
	for bi, ie := range seg.index {
		got, err := seg.decodeBlock(ie.offset)
		if err != nil {
			t.Fatalf("decodeBlock(%d): %v", bi, err)
		}
		lo := bi * blockSize
		for j, w := range got {
			if w != words[lo+j] {
				t.Fatalf("block %d word %d = %q, want %q", bi, j, w, words[lo+j])
			}
		}
	}
}

// TestExactByteLayout verifies the real encoding layout byte-for-byte on
// a known fixture: words ["", "a", "ab", "abc"], blockSize 2.
//
//	Block 0 payload: 02 | 00 | 00 01 61            (5 bytes)
//	Block 1 payload: 02 | 02 61 62 | 02 01 63      (7 bytes)
//	Data section:    (8+5) + (8+7) = 28 bytes
//	Index:           00 00 | 02 61 62 0d           (6 bytes)
//	Total:           28 (header) + 28 + 6 = 62 bytes
func TestExactByteLayout(t *testing.T) {
	seg := buildSegment(t, []string{"", "a", "ab", "abc"}, 2)
	l := seg.Layout()
	if l.HeaderBytes != 28 || l.DataBytes != 28 || l.IndexBytes != 6 || l.TotalBytes != 62 {
		t.Fatalf("layout = %+v, want header=28 data=28 index=6 total=62", l)
	}
	if l.NumWords != 4 || l.NumBlocks != 2 {
		t.Fatalf("layout = %+v, want 4 words in 2 blocks", l)
	}
	if l.NaiveBytes != 4*4+0+1+2+3 {
		t.Fatalf("NaiveBytes = %d, want 22", l.NaiveBytes)
	}
	t.Logf("layout: header=%d data=%d index=%d total=%d bytes for %d words (naive %d bytes)",
		l.HeaderBytes, l.DataBytes, l.IndexBytes, l.TotalBytes, l.NumWords, l.NaiveBytes)
}

// TestLayoutReportsSavings shows real byte occupancy on prefix-heavy data.
func TestLayoutReportsSavings(t *testing.T) {
	var words []string
	for i := 0; i < 20000; i++ {
		words = append(words, fmt.Sprintf("https://www.example.com/articles/2026/09/%08d", i))
	}
	sort.Strings(words)
	seg := buildSegment(t, words, 32)
	l := seg.Layout()
	t.Logf("words=%d blocks=%d total=%dB (header=%d data=%d index=%d) naive=%dB ratio=%.2f",
		l.NumWords, l.NumBlocks, l.TotalBytes, l.HeaderBytes, l.DataBytes, l.IndexBytes, l.NaiveBytes,
		float64(l.TotalBytes)/float64(l.NaiveBytes))
	if l.TotalBytes >= l.NaiveBytes {
		t.Fatalf("expected compression on prefix-heavy data: total=%d naive=%d", l.TotalBytes, l.NaiveBytes)
	}
}
