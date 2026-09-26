package mph

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

func makeKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("user:%08d", i)
	}
	return keys
}

func TestBuildLookupMinimalPerfect(t *testing.T) {
	const n = 50000
	keys := makeKeys(n)
	ix, err := Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ix.Len() != n {
		t.Fatalf("Len = %d, want %d", ix.Len(), n)
	}
	seen := make([]bool, n)
	for _, k := range keys {
		slot, ok := ix.Lookup(k)
		if !ok {
			t.Fatalf("known key %q not found", k)
		}
		if slot >= uint32(n) {
			t.Fatalf("slot %d out of range [0,%d)", slot, n)
		}
		if seen[slot] {
			t.Fatalf("slot %d assigned twice: not collision-free", slot)
		}
		seen[slot] = true
	}
	for i, s := range seen {
		if !s {
			t.Fatalf("slot %d never assigned: not minimal", i)
		}
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	const n = 20000
	keys := makeKeys(n)
	ix, err := Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	probes := 0
	check := func(k string) {
		probes++
		if slot, ok := ix.Lookup(k); ok {
			t.Fatalf("unknown key %q falsely reported as hit at slot %d", k, slot)
		}
	}
	for i := 0; i < 100000; i++ {
		check(fmt.Sprintf("user:%08d", n+i)) // disjoint range
	}
	for _, k := range keys[:1000] {
		check(k + "x")      // extension
		check(k[:len(k)-1]) // truncation
		b := []byte(k)
		b[len(b)/2] ^= 0x01 // one-bit mutation
		check(string(b))
	}
	t.Logf("%d unknown-key probes, zero false hits", probes)
}

func TestDuplicateDiagnosedBeforeBuild(t *testing.T) {
	keys := []string{"delta", "alpha", "delta", "charlie", "alpha", "alpha"}
	ix, err := Build(keys)
	if err == nil {
		t.Fatal("Build with duplicates succeeded, want DuplicateError")
	}
	if ix != nil {
		t.Fatal("Build with duplicates returned an index")
	}
	dup, ok := err.(*DuplicateError)
	if !ok {
		t.Fatalf("error type = %T, want *DuplicateError (%v)", err, err)
	}
	wantKeys := []string{"alpha", "delta"}
	wantCounts := []int{3, 2}
	if len(dup.Keys) != len(wantKeys) {
		t.Fatalf("diagnosed keys = %v, want %v", dup.Keys, wantKeys)
	}
	for i := range wantKeys {
		if dup.Keys[i] != wantKeys[i] || dup.Counts[i] != wantCounts[i] {
			t.Fatalf("diagnosis = %v %v, want %v %v", dup.Keys, dup.Counts, wantKeys, wantCounts)
		}
	}
	t.Logf("diagnostic: %v", err)
}

func TestDeterministicBuild(t *testing.T) {
	keys := makeKeys(5000)
	a, err := Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Same set, different input order: build must be identical.
	shuffled := make([]string, len(keys))
	for i, k := range keys {
		shuffled[len(keys)-1-i] = k
	}
	b, err := Build(shuffled)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if a.Seed() != b.Seed() || a.Attempts() != b.Attempts() {
		t.Fatalf("not reproducible: seed %d vs %d, attempts %d vs %d",
			a.Seed(), b.Seed(), a.Attempts(), b.Attempts())
	}
	if !bytes.Equal(a.MarshalBinary(), b.MarshalBinary()) {
		t.Fatal("encoded bytes differ between identical builds")
	}
	if a.Attempts() < 1 || a.Attempts() > MaxSeedTries {
		t.Fatalf("attempts %d outside bound [1,%d]", a.Attempts(), MaxSeedTries)
	}
	t.Logf("seed=%d attempts=%d (bound %d)", a.Seed(), a.Attempts(), MaxSeedTries)
}

func TestBoundedRetriesExhausted(t *testing.T) {
	keys := makeKeys(1000)
	ix, err := build(keys, 0) // zero seed budget: must fail, not drop keys
	if err == nil {
		t.Fatal("build with zero seed budget succeeded")
	}
	if ix != nil {
		t.Fatal("failed build returned an index")
	}
	inc, ok := err.(*IncompleteError)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteError (%v)", err, err)
	}
	if inc.Keys != len(keys) {
		t.Fatalf("IncompleteError.Keys = %d, want %d (no key may be dropped)", inc.Keys, len(keys))
	}
	t.Logf("diagnostic: %v", err)
}

func TestEmptyIndex(t *testing.T) {
	ix, err := Build(nil)
	if err != nil {
		t.Fatalf("Build(nil): %v", err)
	}
	if ix.Len() != 0 {
		t.Fatalf("Len = %d, want 0", ix.Len())
	}
	if _, ok := ix.Lookup("anything"); ok {
		t.Fatal("empty index reported a hit")
	}
	back, err := Unmarshal(ix.MarshalBinary())
	if err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if back.Len() != 0 {
		t.Fatalf("round-tripped Len = %d, want 0", back.Len())
	}
}

func TestSingleKey(t *testing.T) {
	ix, err := Build([]string{"only"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	slot, ok := ix.Lookup("only")
	if !ok || slot != 0 {
		t.Fatalf("Lookup(only) = (%d,%v), want (0,true)", slot, ok)
	}
	if _, ok := ix.Lookup("onl"); ok {
		t.Fatal("truncated key falsely hit")
	}
	if _, ok := ix.Lookup("onlyy"); ok {
		t.Fatal("extended key falsely hit")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	keys := makeKeys(20000)
	ix, err := Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := ix.MarshalBinary()
	fp := ix.Footprint()
	if len(data) != fp.Total {
		t.Fatalf("encoded %d bytes, Footprint.Total = %d", len(data), fp.Total)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Seed() != ix.Seed() || back.Attempts() != ix.Attempts() {
		t.Fatal("seed/attempts did not survive round trip")
	}
	for _, k := range keys {
		s1, ok1 := ix.Lookup(k)
		s2, ok2 := back.Lookup(k)
		if !ok1 || !ok2 || s1 != s2 {
			t.Fatalf("round trip mismatch for %q: (%d,%v) vs (%d,%v)", k, s1, ok1, s2, ok2)
		}
	}
	if _, ok := back.Lookup("user:99999999"); ok {
		t.Fatal("decoded index falsely hit an unknown key")
	}
	if !bytes.Equal(back.MarshalBinary(), data) {
		t.Fatal("re-encoded bytes differ from original encoding")
	}
}

func TestUnmarshalRejectsCorruption(t *testing.T) {
	ix, err := Build(makeKeys(100))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := ix.MarshalBinary()
	if _, err := Unmarshal(data[:len(data)-1]); err == nil {
		t.Fatal("truncated encoding decoded without error")
	}
	bad := append([]byte(nil), data...)
	bad[0] = 'X'
	if _, err := Unmarshal(bad); err == nil {
		t.Fatal("bad magic decoded without error")
	}
}

func TestSlotsAreContiguousRange(t *testing.T) {
	// Independent check of minimality: slots form exactly {0..n-1}.
	keys := makeKeys(12345)
	ix, err := Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	slots := make([]int, 0, len(keys))
	for _, k := range keys {
		slot, ok := ix.Lookup(k)
		if !ok {
			t.Fatalf("known key %q not found", k)
		}
		slots = append(slots, int(slot))
	}
	sort.Ints(slots)
	for i, s := range slots {
		if s != i {
			t.Fatalf("slots not a contiguous range: position %d has slot %d", i, s)
		}
	}
}
