package crset

import (
	"math/rand"
	"sort"
	"testing"
)

func collect(c *container) []uint16 {
	var out []uint16
	it := c.iter()
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		out = append(out, v)
	}
	return out
}

func sortedSample(rng *rand.Rand, n int) []uint16 {
	m := map[uint16]bool{}
	for len(m) < n {
		m[uint16(rng.Intn(65536))] = true
	}
	out := make([]uint16, 0, n)
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func checkRunInvariant(t *testing.T, c *container) {
	t.Helper()
	card := 0
	for i, r := range c.runs {
		card += int(r.length) + 1
		if i > 0 && uint32(c.runs[i-1].last())+1 >= uint32(r.start) {
			t.Fatalf("runs %d and %d overlap or are adjacent: %v", i-1, i, c.runs)
		}
	}
	if card != c.card {
		t.Fatalf("cardinality mismatch: runs hold %d, card says %d", card, c.card)
	}
}

// TestRunContainerFuzz hammers runAdd/runRemove with random mutations and
// cross-checks every 500 ops against a map reference over the whole domain.
func TestRunContainerFuzz(t *testing.T) {
	c := &container{rep: repRun}
	ref := map[uint16]bool{}
	rng := rand.New(rand.NewSource(7))
	for op := 0; op < 20000; op++ {
		v := uint16(rng.Intn(65536))
		if rng.Intn(100) < 55 {
			if got, want := c.runAdd(v), !ref[v]; got != want {
				t.Fatalf("runAdd(%d) = %v, want %v", v, got, want)
			}
			ref[v] = true
		} else {
			if got, want := c.runRemove(v), ref[v]; got != want {
				t.Fatalf("runRemove(%d) = %v, want %v", v, got, want)
			}
			delete(ref, v)
		}
		if op%500 == 0 {
			checkRunInvariant(t, c)
			for v := 0; v < 65536; v++ {
				if got, want := c.contains(uint16(v)), ref[uint16(v)]; got != want {
					t.Fatalf("contains(%d) = %v, want %v", v, got, want)
				}
			}
		}
	}
}

// TestIteratorsConsistent feeds identical values through all three encodings
// and requires identical ascending iteration.
func TestIteratorsConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	vals := sortedSample(rng, 5000)
	sparse := &container{rep: repSparse, card: len(vals), sparse: append([]uint16(nil), vals...)}
	bitmap := &container{rep: repSparse, card: len(vals), sparse: append([]uint16(nil), vals...)}
	bitmap.toBitmap()
	runc := &container{rep: repSparse, card: len(vals), sparse: append([]uint16(nil), vals...)}
	runc.toRun()
	for name, c := range map[string]*container{"sparse": sparse, "bitmap": bitmap, "run": runc} {
		got := collect(c)
		if len(got) != len(vals) {
			t.Fatalf("%s: iterated %d values, want %d", name, len(got), len(vals))
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Fatalf("%s: position %d = %d, want %d", name, i, got[i], vals[i])
			}
		}
		if got := c.card; got != len(vals) {
			t.Fatalf("%s: card = %d, want %d", name, got, len(vals))
		}
	}
}

func TestRunCount(t *testing.T) {
	build := func(rep repKind) *container {
		c := &container{rep: repSparse}
		for v := uint16(0); v < 100; v++ {
			c.add(v)
		}
		for v := uint16(200); v < 300; v++ {
			c.add(v)
		}
		c.add(65535)
		switch rep {
		case repBitmap:
			c.toBitmap()
		case repRun:
			c.toRun()
		}
		return c
	}
	for _, rep := range []repKind{repSparse, repBitmap, repRun} {
		if got := build(rep).runCount(); got != 3 {
			t.Fatalf("%s: runCount = %d, want 3", rep, got)
		}
	}
}

// TestConversionsPreserveElements walks a container through
// sparse -> bitmap -> sparse, asserting the element sequence is
// bit-identical after every single mutation.
func TestConversionsPreserveElements(t *testing.T) {
	cfg := Config{SparseToBitmapCard: 4096, BitmapToSparseCard: 3584, RunSavingsFactor: 0}
	c := &container{rep: repSparse}
	var ref []uint16
	check := func() {
		t.Helper()
		got := collect(c)
		if len(got) != len(ref) {
			t.Fatalf("rep %s: %d elements, want %d", c.rep, len(got), len(ref))
		}
		for i := range ref {
			if got[i] != ref[i] {
				t.Fatalf("rep %s: position %d = %d, want %d", c.rep, i, got[i], ref[i])
			}
		}
	}
	for i := 0; i <= 5000; i++ {
		v := uint16(3 * i)
		c.add(v)
		c.normalize(cfg)
		ref = append(ref, v)
		check()
	}
	if c.rep != repBitmap {
		t.Fatalf("rep = %s, want bitmap", c.rep)
	}
	for i := 5000; i >= 1500; i-- {
		v := uint16(3 * i)
		c.remove(v)
		c.normalize(cfg)
		ref = ref[:len(ref)-1]
		check()
	}
	if c.rep != repSparse {
		t.Fatalf("rep = %s, want sparse", c.rep)
	}
}
