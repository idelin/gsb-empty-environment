package compactidx_test

import (
	"fmt"
	"testing"

	"compactidx"
	"compactidx/layoutcheck"
)

func buildImage(t *testing.T, n, chunk int) []byte {
	t.Helper()
	kvs := make([]compactidx.KV, n)
	for i := range kvs {
		val := fmt.Sprintf("v%04d", i)
		if i%5 == 0 {
			val = fmt.Sprintf("%s-%0128d", val, i) // force continuation chains
		}
		kvs[i] = compactidx.KV{Key: fmt.Sprintf("k%04d", i), Value: val}
	}
	img, err := compactidx.Build(kvs, chunk)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return img
}

func toLayoutRanges(rs []compactidx.Range) []layoutcheck.Range {
	out := make([]layoutcheck.Range, len(rs))
	for i, r := range rs {
		out[i] = layoutcheck.Range{Start: r.Start, End: r.End}
	}
	return out
}

func TestCheckerAgreesWithValidatedRegions(t *testing.T) {
	img := buildImage(t, 200, 32)
	idx := compactidx.NewIndex()
	if err := idx.Load(img); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer idx.Close()

	checker, err := layoutcheck.New(img)
	if err != nil {
		t.Fatalf("layoutcheck.New: %v", err)
	}
	got := checker.Regions()
	want := toLayoutRanges(idx.ValidatedRegions())
	if len(got) != len(want) {
		t.Fatalf("region count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("region %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestQueriesNeverReadUnvalidatedBytes(t *testing.T) {
	img := buildImage(t, 200, 32)
	idx := compactidx.NewIndex()
	if err := idx.Load(img); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer idx.Close()

	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer v.Close()
	for _, key := range []string{"k0000", "k0100", "k0199", "k0200", "missing"} {
		if _, _, err := v.Get(key); err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
	}
	c, err := v.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	for c.Next() {
	}
	if err := c.Err(); err != nil {
		t.Fatalf("cursor: %v", err)
	}
	c.Close()

	accesses := idx.Accesses()
	if len(accesses) == 0 {
		t.Fatal("no accesses recorded")
	}
	checker, err := layoutcheck.New(img)
	if err != nil {
		t.Fatalf("layoutcheck.New: %v", err)
	}
	if err := checker.Verify(toLayoutRanges(accesses)); err != nil {
		t.Fatalf("independent checker rejected query reads: %v", err)
	}
}

func TestCheckerRejectsUnvalidatedAccess(t *testing.T) {
	img := buildImage(t, 10, 0)
	checker, err := layoutcheck.New(img)
	if err != nil {
		t.Fatalf("layoutcheck.New: %v", err)
	}
	n := uint64(len(img))
	bad := []layoutcheck.Range{
		{Start: n + 512, End: n + 513},  // beyond the file
		{Start: 0, End: n},              // spans many regions
		{Start: 0, End: 1 << 62},        // absurd length
		{Start: ^uint64(0) - 3, End: 2}, // wrapped range
	}
	for _, r := range bad {
		if err := checker.Verify([]layoutcheck.Range{r}); err == nil {
			t.Fatalf("Verify(%v) succeeded, want error", r)
		}
	}
}

func TestCheckerRejectsInvalidImages(t *testing.T) {
	for _, img := range [][]byte{
		nil,
		{},
		[]byte("not an index at all"),
	} {
		if _, err := layoutcheck.New(img); err == nil {
			t.Fatalf("layoutcheck.New(len=%d) succeeded, want error", len(img))
		}
	}
	// Corrupt a valid image: point a table entry beyond the file.
	img := buildImage(t, 4, 0)
	for i := 0; i < 8; i++ {
		img[compactidx.HeaderLen+i] = 0xFF
	}
	if _, err := layoutcheck.New(img); err == nil {
		t.Fatal("layoutcheck.New accepted malicious table offset")
	}
}
