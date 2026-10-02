package stcs

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

const (
	valueSize   = 1000 // ~1KB values
	keysPerTier = 10000
	hotKeys     = 500 // keys rewritten in every batch (updates)
)

// makeBatch generates keysPerTier entries of ~1KB each for one flush.
// Keys are mostly disjoint per batch (key id = batch*keysPerTier + i);
// the first hotKeys ids of every batch map to a shared set of "hot"
// keys, producing duplicate updates across batches. Each value embeds
// its batch number so tests can verify the newest version wins.
func makeBatch(rnd *rand.Rand, batch int) []Entry {
	return makeBatchN(rnd, batch, keysPerTier)
}

func makeBatchN(rnd *rand.Rand, batch, nEntries int) []Entry {
	entries := make([]Entry, nEntries)
	for i := range entries {
		keyID := batch*nEntries + i
		if i < hotKeys {
			keyID = i // shared hot key, updated every batch
		}
		key := []byte(fmt.Sprintf("key-%08d", keyID))
		val := make([]byte, valueSize)
		rnd.Read(val)
		binary.BigEndian.PutUint64(val[:8], uint64(batch))
		entries[i] = Entry{Key: key, Value: val}
	}
	return entries
}

func batchOf(val []byte) uint64 { return binary.BigEndian.Uint64(val[:8]) }

func readAll(t *testing.T, path string) map[string]Entry {
	t.Helper()
	it, err := OpenIterator(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer it.Close()
	out := make(map[string]Entry)
	for it.Next() {
		e := it.Entry()
		out[string(e.Key)] = e
	}
	if it.Err() != nil {
		t.Fatalf("iterate %s: %v", path, it.Err())
	}
	return out
}

func newTempCompactor(t *testing.T, opts Options) (*Compactor, string) {
	t.Helper()
	dir, err := ioutil.TempDir("", "stcs-test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCompactor(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

// TestFourSimilarTablesMergeIntoOne is the core acceptance test:
// sustained writes of 4 x ~10MB SSTables in one tier trigger a merge
// into a single ~40MB table; duplicate keys keep only the newest
// version; write amplification stays within the theoretical bound.
func TestFourSimilarTablesMergeIntoOne(t *testing.T) {
	c, dir := newTempCompactor(t, Options{Fanout: 4})
	defer os.RemoveAll(dir)

	rnd := rand.New(rand.NewSource(42))
	const batches = 4

	for b := 0; b < batches; b++ {
		tbl, err := c.Write(makeBatch(rnd, b))
		if err != nil {
			t.Fatalf("write batch %d: %v", b, err)
		}
		t.Logf("batch %d flushed: %s (%.2f MB)", b, filepath.Base(tbl.Path), float64(tbl.Size)/(1<<20))
		if b < batches-1 {
			if got := len(c.Tables(0)); got != b+1 {
				t.Fatalf("after batch %d: tier0 has %d tables, want %d (no early merge)", b, got, b+1)
			}
		}
	}

	// Merge triggered: tier 0 drained, tier 1 holds exactly one table.
	if got := len(c.Tables(0)); got != 0 {
		t.Fatalf("tier0 still has %d tables after merge", got)
	}
	merged := c.Tables(1)
	if len(merged) != 1 {
		t.Fatalf("tier1 has %d tables, want 1", len(merged))
	}
	out := merged[0]
	t.Logf("merged output: %s (%.2f MB), %d entries",
		filepath.Base(out.Path), float64(out.Size)/(1<<20), out.Entries)
	if out.Size < 35*(1<<20) || out.Size > 45*(1<<20) {
		t.Fatalf("merged size %.2f MB outside expected ~40MB band", float64(out.Size)/(1<<20))
	}

	// Every key appears exactly once; hot keys hold the batch-3 value.
	wantKeys := batches*keysPerTier - hotKeys*(batches-1)
	got := readAll(t, out.Path)
	if len(got) != wantKeys {
		t.Fatalf("merged table has %d distinct keys, want %d", len(got), wantKeys)
	}
	for i := 0; i < hotKeys; i++ {
		key := fmt.Sprintf("key-%08d", i)
		e, ok := got[key]
		if !ok {
			t.Fatalf("hot key %s missing from merged table", key)
		}
		if b := batchOf(e.Value); b != batches-1 {
			t.Fatalf("hot key %s: value from batch %d, want newest batch %d", key, b, batches-1)
		}
	}
	// Spot-check cold keys carry their originating batch.
	for _, probe := range []int{hotKeys, keysPerTier - 1, keysPerTier + hotKeys, 3*keysPerTier - 1} {
		key := fmt.Sprintf("key-%08d", probe)
		e, ok := got[key]
		if !ok {
			t.Fatalf("cold key %s missing from merged table", key)
		}
		if want := uint64(probe / keysPerTier); batchOf(e.Value) != want {
			t.Fatalf("cold key %s: value from batch %d, want %d", key, batchOf(e.Value), want)
		}
	}

	// Amplification control lines after exactly one merge round:
	//   WA = (flush + compaction writes) / logical <= 2.0
	//   RA = compaction reads / logical ~= 1.0
	wa := c.Stats.WriteAmplification()
	ra := c.Stats.ReadAmplification()
	t.Logf("logical=%.2fMB flush=%.2fMB compactRead=%.2fMB compactWrite=%.2fMB WA=%.3f RA=%.3f",
		float64(c.Stats.LogicalBytes)/(1<<20), float64(c.Stats.FlushBytes)/(1<<20),
		float64(c.Stats.CompactionReadBytes)/(1<<20), float64(c.Stats.CompactionWriteBytes)/(1<<20),
		wa, ra)
	if wa > 2.0 {
		t.Fatalf("write amplification %.3f exceeds theoretical control line 2.0", wa)
	}
	if ra > 1.05 {
		t.Fatalf("read amplification %.3f exceeds expected ~1.0", ra)
	}
	if c.Stats.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", c.Stats.Compactions)
	}
}

// TestNoMergeBelowFanout verifies that fewer than N tables never merge.
func TestNoMergeBelowFanout(t *testing.T) {
	c, dir := newTempCompactor(t, Options{Fanout: 4})
	defer os.RemoveAll(dir)

	rnd := rand.New(rand.NewSource(1))
	for b := 0; b < 3; b++ {
		if _, err := c.Write(makeBatch(rnd, b)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(c.Tables(0)); got != 3 {
		t.Fatalf("tier0 has %d tables, want 3 (no merge below fanout)", got)
	}
	if c.Stats.Compactions != 0 {
		t.Fatalf("unexpected compaction below fanout")
	}
}

// TestSizeSimilarityGating verifies that a table much smaller than the
// others is not swept into a merge; the four ~equal tables merge alone.
func TestSizeSimilarityGating(t *testing.T) {
	c, dir := newTempCompactor(t, Options{Fanout: 4, SizeSimilarity: 1.5})
	defer os.RemoveAll(dir)

	rnd := rand.New(rand.NewSource(7))
	// One tiny table, then four ~10MB tables.
	tiny := make([]Entry, 100)
	for i := range tiny {
		tiny[i] = Entry{Key: []byte(fmt.Sprintf("tiny-%06d", i)), Value: make([]byte, 16)}
	}
	if _, err := c.Write(tiny); err != nil {
		t.Fatal(err)
	}
	for b := 0; b < 4; b++ {
		if _, err := c.Write(makeBatch(rnd, b)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(c.Tables(0)); got != 1 {
		t.Fatalf("tier0 has %d tables, want 1 (the tiny table stays)", got)
	}
	if got := len(c.Tables(1)); got != 1 {
		t.Fatalf("tier1 has %d tables, want 1 merged table", got)
	}
	if c.Stats.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", c.Stats.Compactions)
	}
}

// TestHighFrequencyWritesCascade exercises sustained high-frequency
// writes: 16 small batches cascade through tier 0 -> 1 -> 2, and the
// write amplification stays within the multi-tier control line
// WA <= 1 + numberOfTiersRewritten.
func TestHighFrequencyWritesCascade(t *testing.T) {
	c, dir := newTempCompactor(t, Options{Fanout: 4})
	defer os.RemoveAll(dir)

	rnd := rand.New(rand.NewSource(99))
	for b := 0; b < 16; b++ {
		if _, err := c.Write(makeBatchN(rnd, b, 2000)); err != nil {
			t.Fatalf("batch %d: %v", b, err)
		}
	}

	// 16 flushes of fanout 4: tier0 -> 4 merges into tier1,
	// tier1 -> 1 merge into tier2. Final shape: one table in tier 2.
	if got := len(c.Tables(0)); got != 0 {
		t.Fatalf("tier0 has %d tables, want 0", got)
	}
	if got := len(c.Tables(1)); got != 0 {
		t.Fatalf("tier1 has %d tables, want 0", got)
	}
	if got := len(c.Tables(2)); got != 1 {
		t.Fatalf("tier2 has %d tables, want 1", len(c.Tables(2)))
	}
	if c.Stats.Compactions != 5 {
		t.Fatalf("compactions = %d, want 5 (4x tier0 + 1x tier1)", c.Stats.Compactions)
	}

	wa := c.Stats.WriteAmplification()
	t.Logf("cascade: compactions=%d WA=%.3f RA=%.3f", c.Stats.Compactions, wa, c.Stats.ReadAmplification())
	// Data passes at most two merge tiers: WA <= 1 (flush) + 2 (rewrites).
	if wa > 3.0 {
		t.Fatalf("write amplification %.3f exceeds two-tier control line 3.0", wa)
	}
}

// TestTombstonesDropped verifies deletes are honored and tombstones are
// reclaimed during compaction.
func TestTombstonesDropped(t *testing.T) {
	c, dir := newTempCompactor(t, Options{Fanout: 2, SizeSimilarity: 100})
	defer os.RemoveAll(dir)

	rnd := rand.New(rand.NewSource(3))
	if _, err := c.Write(makeBatch(rnd, 0)); err != nil {
		t.Fatal(err)
	}
	// Second batch deletes every key of the first.
	tombs := make([]Entry, keysPerTier)
	for i := range tombs {
		keyID := i
		if i < hotKeys {
			keyID = i
		}
		tombs[i] = Entry{Key: []byte(fmt.Sprintf("key-%08d", keyID)), Tombstone: true}
	}
	if _, err := c.Write(tombs); err != nil {
		t.Fatal(err)
	}

	merged := c.Tables(1)
	if len(merged) != 1 {
		t.Fatalf("tier1 has %d tables, want 1", len(merged))
	}
	if merged[0].Entries != 0 {
		t.Fatalf("merged table has %d entries, want 0 (all deleted)", merged[0].Entries)
	}
	if got := len(readAll(t, merged[0].Path)); got != 0 {
		t.Fatalf("merged table yields %d keys, want 0", got)
	}
}
