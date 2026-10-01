package blkdict

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// randKeys generates n unique sorted keys over a mixed ASCII/CJK alphabet,
// always including the empty string as the first key. It is the independent
// reference array every query is checked against.
func randKeys(r *rand.Rand, n int) []string {
	alphabet := []rune("abcdelmopstz\x00\xe4\xb8\xad\xe6\x96\x87\xe5\xad\x97\xe6\xb5\x8b")
	set := map[string]bool{"": true}
	for len(set) < n {
		l := r.Intn(12)
		var sb strings.Builder
		for i := 0; i < l; i++ {
			sb.WriteRune(alphabet[r.Intn(len(alphabet))])
		}
		set[sb.String()] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func naivePrefixRange(keys []string, prefix string) []string {
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

func TestRandomAccessAgainstReferenceArray(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	ref := randKeys(r, 500)
	st, err := NewStore(ref)
	if err != nil {
		t.Fatal(err)
	}
	if st.Len() != len(ref) {
		t.Fatalf("Len=%d want %d", st.Len(), len(ref))
	}
	// Full scan: every k-th word must equal the reference array.
	for i, want := range ref {
		got, err := st.Get(i)
		if err != nil {
			t.Fatalf("Get(%d): %v", i, err)
		}
		if got != want {
			t.Fatalf("Get(%d)=%q want %q", i, got, want)
		}
	}
	// Random reads, and decode-count bound.
	for i := 0; i < 300; i++ {
		k := r.Intn(len(ref))
		got, err := st.Get(k)
		if err != nil || got != ref[k] {
			t.Fatalf("Get(%d)=%q,%v want %q", k, got, err, ref[k])
		}
	}
	stats := st.Stats()
	if stats.MaxDecodedPerRead < 1 || stats.MaxDecodedPerRead > BlockSize {
		t.Fatalf("MaxDecodedPerRead=%d outside [1,%d]", stats.MaxDecodedPerRead, BlockSize)
	}
	if _, err := st.Get(len(ref)); err == nil {
		t.Fatal("Get out of range must fail")
	}
	if _, err := st.Get(-1); err == nil {
		t.Fatal("Get(-1) must fail")
	}
}

func TestPrefixRangeAgainstReferenceArray(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	ref := randKeys(r, 400)
	st, err := NewStore(ref)
	if err != nil {
		t.Fatal(err)
	}
	prefixes := []string{"", "a", "ab", "中", "测"}
	// Byte prefixes of random stored words, including prefixes that split a
	// UTF-8 sequence: matching is byte-wise, output must stay whole strings.
	for i := 0; i < 60; i++ {
		w := ref[r.Intn(len(ref))]
		if len(w) > 0 {
			prefixes = append(prefixes, w[:r.Intn(len(w)+1)])
		}
	}
	for _, p := range prefixes {
		got, err := st.PrefixRange(p)
		if err != nil {
			t.Fatalf("PrefixRange(%q): %v", p, err)
		}
		want := naivePrefixRange(ref, p)
		if len(got) != len(want) {
			t.Fatalf("PrefixRange(%q) returned %d keys, want %d", p, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("PrefixRange(%q)[%d]=%q want %q", p, i, got[i], want[i])
			}
			if !utf8.ValidString(got[i]) {
				t.Fatalf("PrefixRange(%q)[%d]=%q is not valid UTF-8", p, i, got[i])
			}
		}
	}
}

func TestEmptyStringFirstKey(t *testing.T) {
	st, err := NewStore([]string{"", "a", "ab", "b"})
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.Get(0)
	if err != nil || k != "" {
		t.Fatalf("Get(0)=%q,%v want empty string", k, err)
	}
	all, err := st.PrefixRange("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0] != "" {
		t.Fatalf("PrefixRange(\"\")=%q", all)
	}
}

func TestUTF8BytePrefixWholeStringOutput(t *testing.T) {
	keys := []string{"中文", "中国", "中文字典", "字", "字节", "测试"}
	st, err := NewStore(keys)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.PrefixRange("中")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "中国" || got[1] != "中文" || got[2] != "中文字典" {
		t.Fatalf("PrefixRange(中)=%q", got)
	}
	// Prefix that splits the UTF-8 encoding of 中 (E4 B8 AD): byte-wise
	// match, but every returned word is a complete stored string.
	got, err = st.PrefixRange("\xe4\xb8")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("PrefixRange(split UTF-8)=%q want 3 keys", got)
	}
	for _, k := range got {
		if !utf8.ValidString(k) || !strings.HasPrefix(k, "中") {
			t.Fatalf("bad key %q", k)
		}
	}
}

func TestDuplicatePolicy(t *testing.T) {
	if _, err := NewStore([]string{"a", "b", "a"}); err == nil {
		t.Fatal("duplicates at build must be rejected")
	}
	st, err := NewStore([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyBatch([]string{"x", "x"}); err == nil {
		t.Fatal("duplicates within a batch must be rejected")
	}
	// Re-adding an existing key is idempotent.
	if err := st.ApplyBatch([]string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	if st.Len() != 3 {
		t.Fatalf("Len=%d want 3 (a,b,c)", st.Len())
	}
}

// fixCRC recomputes a block's CRC after deliberate corruption, so tests hit
// the length-field validators rather than the checksum.
func fixCRC(block []byte) {
	binary.BigEndian.PutUint32(block[len(block)-4:], crc32.ChecksumIEEE(block[:len(block)-4]))
}

func TestCorruptionNeverSilentlyMisdecoded(t *testing.T) {
	keys := []string{"apple", "application", "apply", "banana"}
	block := EncodeBlock(keys)

	// 1. Any single-bit flip without CRC fix must fail the checksum.
	for off := 0; off < len(block); off++ {
		bad := append([]byte(nil), block...)
		bad[off] ^= 0x01
		if _, err := DecodeBlock(bad); err == nil {
			t.Fatalf("flip at offset %d not detected", off)
		}
	}

	// 2. Corrupt prefix length (with valid CRC) must be rejected BEFORE
	// allocation: entry 1 shares 3 bytes with "apple"; claim 1<<20 instead.
	bad := append([]byte(nil), block...)
	hdr := blockHdrLen + len("apple")
	if bad[hdr] != 4 { // sanity: prefixLen of "application" vs "apple" ("appl")
		t.Fatalf("layout assumption broken: prefixLen byte=%d", bad[hdr])
	}
	n := binary.PutUvarint(bad[hdr:], 1<<20)
	_ = n
	fixCRC(bad)
	if _, err := DecodeBlock(bad); err == nil {
		t.Fatal("oversized prefix length accepted")
	} else if _, ok := err.(*CorruptError); !ok {
		t.Fatalf("want *CorruptError, got %T", err)
	}

	// 3. Prefix length one past the previous key is also rejected: it may
	// never reference bytes beyond the previous key of the same block.
	bad2 := append([]byte(nil), block...)
	bad2[hdr] = byte(len("apple") + 1)
	fixCRC(bad2)
	if _, err := DecodeBlock(bad2); err == nil {
		t.Fatal("prefix length beyond previous key accepted")
	}

	// 4. Truncation and garbage suffix length are rejected.
	if _, err := DecodeBlock(block[:len(block)-6]); err == nil {
		t.Fatal("truncated block accepted")
	}
	bad3 := append([]byte(nil), block...)
	bad3[hdr+1] = 0x7f // suffixLen of entry 1 far beyond block remainder
	fixCRC(bad3)
	if _, err := DecodeBlock(bad3); err == nil {
		t.Fatal("oversized suffix length accepted")
	}
}

func TestCorruptionDoesNotLeakAcrossBlocks(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	ref := randKeys(r, 3*BlockSize)
	st, err := NewStore(ref)
	if err != nil {
		t.Fatal(err)
	}
	seg := st.active
	// Corrupt the last byte of block 0's payload (valid CRC is impossible to
	// forge here, which is the point) and verify block 1+ still decode.
	seg.data[seg.blockOff[1]-1] ^= 0xff
	if _, err := st.Get(0); err == nil {
		t.Fatal("corrupt block 0 must fail")
	}
	for _, k := range []int{BlockSize, 2*BlockSize - 1, 2 * BlockSize} {
		got, err := st.Get(k)
		if err != nil || got != ref[k] {
			t.Fatalf("Get(%d)=%q,%v after block-0 corruption; blocks must decode independently", k, got, err)
		}
	}
}

func TestBatchUpdateMergeAndSnapshot(t *testing.T) {
	st, err := NewStore([]string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if err := st.ApplyBatch([]string{"d", "e", "f"}); err != nil {
		t.Fatal(err)
	}
	if st.Len() != 6 {
		t.Fatalf("store Len=%d want 6", st.Len())
	}
	// Snapshot still sees the old view.
	if snap.Len() != 3 {
		t.Fatalf("snapshot Len=%d want 3", snap.Len())
	}
	if k, _ := snap.Get(2); k != "c" {
		t.Fatalf("snapshot Get(2)=%q want c", k)
	}
	if got, _ := snap.PrefixRange("d"); len(got) != 0 {
		t.Fatalf("snapshot must not see batch keys: %q", got)
	}
	if got, _ := st.PrefixRange("d"); len(got) != 1 || got[0] != "d" {
		t.Fatalf("store PrefixRange(d)=%q", got)
	}
	// Reclamation condition: replaced in active view AND no snapshot pins it.
	if got := st.Stats().SegmentsReclaimed; got != 0 {
		t.Fatalf("segment reclaimed while snapshot open: %d", got)
	}
	snap.Close()
	if got := st.Stats().SegmentsReclaimed; got != 1 {
		t.Fatalf("SegmentsReclaimed=%d want 1 after last pin dropped", got)
	}
	// Next batch reclaims the previous active segment immediately.
	if err := st.ApplyBatch([]string{"g"}); err != nil {
		t.Fatal(err)
	}
	if got := st.Stats().SegmentsReclaimed; got != 2 {
		t.Fatalf("SegmentsReclaimed=%d want 2", got)
	}
	if st.Len() != 7 {
		t.Fatalf("Len=%d want 7", st.Len())
	}
}

func TestMaxDecodedPerRandomRead(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	ref := randKeys(r, 10*BlockSize)
	st, err := NewStore(ref)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := st.Get(r.Intn(len(ref))); err != nil {
			t.Fatal(err)
		}
	}
	// Reading the last entry of a full block decodes exactly BlockSize entries.
	if _, err := st.Get(BlockSize - 1); err != nil {
		t.Fatal(err)
	}
	stats := st.Stats()
	if stats.MaxDecodedPerRead != BlockSize {
		t.Fatalf("MaxDecodedPerRead=%d want %d", stats.MaxDecodedPerRead, BlockSize)
	}
	if stats.RandomReads != 501 {
		t.Fatalf("RandomReads=%d want 501", stats.RandomReads)
	}
}
