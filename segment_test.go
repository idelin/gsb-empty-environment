package invindex

import (
	"math/rand"
	"reflect"
	"testing"
)

// buildAcceptanceSegment builds the acceptance-test index: 20000 documents,
// every document contains "storage", only every 800th contains "database".
// The two posting lists are therefore extremely skewed (25 vs 20000).
func buildAcceptanceSegment() *Segment {
	b := NewBuilder()
	for i := 0; i < 20000; i++ {
		text := "storage"
		if i%800 == 0 {
			text = "database storage"
		}
		b.AddDocument(uint64(i), text)
	}
	return b.Build()
}

func expectedAcceptanceDocs() []uint64 {
	var docs []uint64
	for i := 0; i < 20000; i += 800 {
		docs = append(docs, uint64(i))
	}
	return docs
}

func TestAcceptanceDatabaseAndStorage(t *testing.T) {
	seg := buildAcceptanceSegment()

	db, ok := seg.Postings("database")
	if !ok {
		t.Fatal("term 'database' not found in dictionary")
	}
	st, ok := seg.Postings("storage")
	if !ok {
		t.Fatal("term 'storage' not found in dictionary")
	}
	if db.Len() != 25 || st.Len() != 20000 {
		t.Fatalf("posting list lengths = %d and %d, want 25 and 20000", db.Len(), st.Len())
	}

	matches := seg.And("database", "storage")
	var got []uint64
	for _, m := range matches {
		got = append(got, m.DocID)
		if len(m.Freqs) != 2 || m.Freqs[0] != 1 || m.Freqs[1] != 1 {
			t.Fatalf("doc %d: freqs = %v, want [1 1]", m.DocID, m.Freqs)
		}
	}
	if !reflect.DeepEqual(got, expectedAcceptanceDocs()) {
		t.Fatalf("intersection = %v, want %v", got, expectedAcceptanceDocs())
	}
}

func TestSkipPointersAccelerateSkewedIntersection(t *testing.T) {
	seg := buildAcceptanceSegment()
	db, _ := seg.Postings("database")
	st, _ := seg.Postings("storage")

	fast, fastStats := AndStats(db, st)
	slow, slowStats := AndNaive(db, st)

	if !reflect.DeepEqual(fast, slow) {
		t.Fatal("skip-pointer and naive intersections disagree")
	}
	if fastStats.Skips == 0 {
		t.Fatal("expected skip pointers to be used")
	}
	// The naive merge linearly scans the long list up to the last matching
	// doc (19200), decoding tens of thousands of postings; the skip-pointer
	// intersection must decode drastically fewer.
	if slowStats.Decoded < 15000 {
		t.Fatalf("naive intersection decoded %d postings, want >= 15000", slowStats.Decoded)
	}
	if fastStats.Decoded >= slowStats.Decoded/10 {
		t.Fatalf("skip intersection decoded %d postings, naive %d: less than 10x speedup",
			fastStats.Decoded, slowStats.Decoded)
	}
	t.Logf("skip-pointer intersection: decoded=%d skips=%d; naive: decoded=%d",
		fastStats.Decoded, fastStats.Skips, slowStats.Decoded)
}

func TestPrefixCompressedDictionary(t *testing.T) {
	b := NewBuilder()
	terms := []string{
		"data", "database", "databases", "datum",
		"storage", "store", "stored", "stores", "storing",
		"index", "inverted", "segment", "skip", "pointer",
	}
	for i, term := range terms {
		for d := 0; d <= i; d++ {
			b.AddTerm(uint64(d*7+1), term)
		}
	}
	seg := b.Build()
	if seg.NumTerms() != len(terms) {
		t.Fatalf("NumTerms = %d, want %d", seg.NumTerms(), len(terms))
	}
	for i, term := range terms {
		pl, ok := seg.Postings(term)
		if !ok {
			t.Fatalf("term %q not found", term)
		}
		if pl.Len() != i+1 {
			t.Fatalf("term %q: list length = %d, want %d", term, pl.Len(), i+1)
		}
	}
	for _, absent := range []string{"", "d", "databas", "databaseX", "st", "zebra", "point"} {
		if _, ok := seg.Postings(absent); ok {
			t.Fatalf("term %q unexpectedly found", absent)
		}
	}
}

func TestPostingListRoundTrip(t *testing.T) {
	b := NewBuilder()
	docs := []uint64{3, 10, 11, 500, 9999}
	freqs := []uint32{1, 2, 5, 1, 3}
	for i, d := range docs {
		f := freqs[i]
		for i := uint32(0); i < f; i++ {
			b.AddTerm(d, "x")
		}
	}
	seg := b.Build()
	pl, ok := seg.Postings("x")
	if !ok {
		t.Fatal("term 'x' not found")
	}
	it := pl.Iterator()
	for i, d := range docs {
		if !it.Next() {
			t.Fatalf("iterator exhausted before doc %d", d)
		}
		if it.Doc() != d || it.Freq() != freqs[i] {
			t.Fatalf("got (doc=%d, freq=%d), want (%d, %d)", it.Doc(), it.Freq(), d, freqs[i])
		}
	}
	if it.Next() {
		t.Fatal("iterator should be exhausted")
	}
}

// TestIntersectMatchesNaiveOnRandomData cross-checks the skip-pointer
// intersection against the naive linear merge on random skewed lists.
func TestIntersectMatchesNaiveOnRandomData(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		b := NewBuilder()
		// Short list: a handful of docs. Long list: dense, thousands of docs.
		var shortDocs []uint64
		for d := uint64(0); d < 50000; d++ {
			if rng.Intn(2000) == 0 {
				shortDocs = append(shortDocs, d)
			}
			if rng.Intn(3) != 0 {
				b.AddTerm(d, "long")
			}
		}
		for _, d := range shortDocs {
			b.AddTerm(d, "short")
		}
		seg := b.Build()
		short, _ := seg.Postings("short")
		long, _ := seg.Postings("long")

		fast, _ := AndStats(short, long)
		slow, _ := AndNaive(short, long)
		if !reflect.DeepEqual(fast, slow) {
			t.Fatalf("trial %d: skip-pointer and naive intersections disagree", trial)
		}
	}
}

func TestAdvanceCeilingExact(t *testing.T) {
	b := NewBuilder()
	docs := []uint64{5, 9, 100, 101, 102, 4000, 4001, 9000}
	for _, d := range docs {
		b.AddTerm(d, "t")
	}
	seg := b.Build()
	pl, _ := seg.Postings("t")

	ceil := func(target uint64) (uint64, bool) {
		for _, d := range docs {
			if d >= target {
				return d, true
			}
		}
		return 0, false
	}
	it := pl.Iterator()
	for target := uint64(0); target <= 12000; target += 333 {
		wantDoc, ok := ceil(target)
		// Restart the iterator from the beginning for an exact check.
		it = pl.Iterator()
		got := it.Advance(target)
		if got != ok {
			t.Fatalf("Advance(%d) = %v, want %v", target, got, ok)
		}
		if ok && it.Doc() != wantDoc {
			t.Fatalf("Advance(%d) landed on %d, want %d", target, it.Doc(), wantDoc)
		}
	}
}

func TestAndWithMissingTerm(t *testing.T) {
	seg := buildAcceptanceSegment()
	if m := seg.And("database", "nonexistent"); m != nil {
		t.Fatalf("And with missing term = %v, want nil", m)
	}
}

func TestSegmentSizeIsCompact(t *testing.T) {
	seg := buildAcceptanceSegment()
	// 20000 delta+freq varint pairs, a 25-entry list and a small dictionary
	// must fit in well under 100 KB.
	if seg.Size() > 100*1024 {
		t.Fatalf("segment size = %d bytes, want < 100 KB", seg.Size())
	}
	t.Logf("segment: %d docs, %d terms, dict=%d B, postings=%d B",
		seg.NumDocs(), seg.NumTerms(), seg.DictBytes(), seg.PostingBytes())
}
