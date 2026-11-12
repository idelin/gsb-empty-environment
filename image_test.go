package netsec

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func sourceOf(data string) io.ReaderAt { return bytes.NewReader([]byte(data)) }

func fill(b byte, n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}

func mustDeclare(t *testing.T, im *Image, seg Segment) {
	t.Helper()
	if err := im.Declare(seg); err != nil {
		t.Fatalf("Declare(%+v): %v", seg, err)
	}
}

func TestOriginalReadWithProvenance(t *testing.T) {
	im := New()
	if err := im.AddSource("disk0", sourceOf("0123456789ABCDEF")); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 100, Len: 16}, Kind: Original, Source: "disk0", SrcOff: 0,
	})

	buf := make([]byte, 4)
	n, frags, err := im.ReadWithProvenance(buf, 104)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || string(buf) != "4567" {
		t.Fatalf("got %d bytes %q", n, buf)
	}
	want := []Fragment{{Source: "disk0", Span: Span{Off: 4, Len: 4}}}
	if !reflect.DeepEqual(frags, want) {
		t.Fatalf("fragments = %+v, want %+v", frags, want)
	}
}

func TestMissingReadFails(t *testing.T) {
	im := New()
	mustDeclare(t, im, Segment{Span: Span{Off: 0, Len: 64}, Kind: Missing})

	_, _, err := im.ReadWithProvenance(make([]byte, 8), 16)
	var merr *MissingError
	if !errors.As(err, &merr) || !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v, want MissingError", err)
	}
	if merr.Span != (Span{Off: 16, Len: 8}) {
		t.Fatalf("missing span = %+v", merr.Span)
	}
	// Undeclared regions are missing too.
	if _, _, err := im.ReadWithProvenance(make([]byte, 8), 1000); !errors.Is(err, ErrMissing) {
		t.Fatalf("undeclared read err = %v", err)
	}
}

func TestDerivedChainResolvesToOriginals(t *testing.T) {
	im := New()
	if err := im.AddSource("disk0", sourceOf(string(fill('a', 256)))); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 256}, Kind: Original, Source: "disk0",
	})
	mustDeclare(t, im, Segment{
		Span: Span{Off: 256, Len: 64}, Kind: Derived,
		Deps: []Span{{Off: 0, Len: 128}},
		Data: sourceOf(string(fill('b', 64))),
	})
	mustDeclare(t, im, Segment{
		Span: Span{Off: 512, Len: 32}, Kind: Derived,
		Deps: []Span{{Off: 256, Len: 64}, {Off: 128, Len: 64}},
		Data: sourceOf(string(fill('c', 32))),
	})

	buf := make([]byte, 32)
	if _, frags, err := im.ReadWithProvenance(buf, 512); err != nil {
		t.Fatal(err)
	} else {
		if !bytes.Equal(buf, fill('c', 32)) {
			t.Fatalf("derived bytes = %q", buf)
		}
		want := []Fragment{{Source: "disk0", Span: Span{Off: 0, Len: 192}}}
		if !reflect.DeepEqual(frags, want) {
			t.Fatalf("fragments = %+v, want %+v", frags, want)
		}
	}
}

func TestCycleRejected(t *testing.T) {
	im := New()
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 100}, Kind: Derived,
		Deps: []Span{{Off: 100, Len: 100}},
		Data: sourceOf(string(fill('a', 100))),
	})
	// Two-cycle: B depends on A's span, A already depends on B's span.
	err := im.Declare(Segment{
		Span: Span{Off: 100, Len: 100}, Kind: Derived,
		Deps: []Span{{Off: 0, Len: 100}},
		Data: sourceOf(string(fill('b', 100))),
	})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("two-cycle err = %v", err)
	}
	// Self-dependency.
	err = im.Declare(Segment{
		Span: Span{Off: 200, Len: 100}, Kind: Derived,
		Deps: []Span{{Off: 250, Len: 10}},
		Data: sourceOf(string(fill('c', 100))),
	})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("self-cycle err = %v", err)
	}
	// Rejected declarations must not have stuck.
	if _, _, err := im.ReadWithProvenance(make([]byte, 4), 100); !errors.Is(err, ErrMissing) {
		t.Fatalf("rejected segment readable, err = %v", err)
	}
	// Indirect three-cycle: C -> D, D -> E, then E -> C rejected.
	mustDeclare(t, im, Segment{
		Span: Span{Off: 300, Len: 50}, Kind: Derived,
		Deps: []Span{{Off: 350, Len: 50}},
		Data: sourceOf(string(fill('d', 50))),
	})
	mustDeclare(t, im, Segment{
		Span: Span{Off: 350, Len: 50}, Kind: Derived,
		Deps: []Span{{Off: 400, Len: 50}},
		Data: sourceOf(string(fill('e', 50))),
	})
	err = im.Declare(Segment{
		Span: Span{Off: 400, Len: 50}, Kind: Derived,
		Deps: []Span{{Off: 300, Len: 50}},
		Data: sourceOf(string(fill('f', 50))),
	})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("three-cycle err = %v", err)
	}
}

func TestSharedDependenciesMinimalFragmentSet(t *testing.T) {
	im := New()
	if err := im.AddSource("disk0", sourceOf(string(fill('a', 128)))); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 128}, Kind: Original, Source: "disk0",
	})
	// Two derived blocks share the same dependency interval.
	mustDeclare(t, im, Segment{
		Span: Span{Off: 128, Len: 64}, Kind: Derived,
		Deps: []Span{{Off: 0, Len: 128}},
		Data: sourceOf(string(fill('b', 64))),
	})
	mustDeclare(t, im, Segment{
		Span: Span{Off: 192, Len: 64}, Kind: Derived,
		Deps: []Span{{Off: 0, Len: 128}},
		Data: sourceOf(string(fill('c', 64))),
	})

	_, frags, err := im.ReadWithProvenance(make([]byte, 128), 128)
	if err != nil {
		t.Fatal(err)
	}
	// The shared dependency must appear exactly once: the minimal set.
	want := []Fragment{{Source: "disk0", Span: Span{Off: 0, Len: 128}}}
	if !reflect.DeepEqual(frags, want) {
		t.Fatalf("fragments = %+v, want %+v", frags, want)
	}
}

// Overlapping coverage: after an original segment overrides part of a
// derived block, cached provenance of the derived block must not leak
// onto the overridden range. The cache never promotes an unverified
// resolution into original evidence.
func TestOverlapCacheDoesNotPromoteUnverifiedSource(t *testing.T) {
	im := New()
	if err := im.AddSource("alpha", sourceOf(string(fill('a', 100)))); err != nil {
		t.Fatal(err)
	}
	if err := im.AddSource("beta", sourceOf(string(fill('b', 64)))); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 100, Len: 100}, Kind: Original, Source: "alpha",
	})
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 100}, Kind: Derived,
		Deps: []Span{{Off: 100, Len: 100}},
		Data: sourceOf(string(fill('d', 100))),
	})

	// Populate the cache through the derived block.
	_, frags, err := im.ReadWithProvenance(make([]byte, 100), 0)
	if err != nil {
		t.Fatal(err)
	}
	wantAlpha := []Fragment{{Source: "alpha", Span: Span{Off: 0, Len: 100}}}
	if !reflect.DeepEqual(frags, wantAlpha) {
		t.Fatalf("fragments = %+v, want %+v", frags, wantAlpha)
	}

	// Original evidence overrides the first half of the derived block.
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 64}, Kind: Original, Source: "beta",
	})

	// The overridden range must attribute beta, never the cached alpha.
	buf := make([]byte, 64)
	if _, frags, err := im.ReadWithProvenance(buf, 0); err != nil {
		t.Fatal(err)
	} else {
		if !bytes.Equal(buf, fill('b', 64)) {
			t.Fatalf("bytes = %q, want beta content", buf)
		}
		want := []Fragment{{Source: "beta", Span: Span{Off: 0, Len: 64}}}
		if !reflect.DeepEqual(frags, want) {
			t.Fatalf("fragments = %+v, want %+v", frags, want)
		}
	}

	// The still-derived tail keeps attributing alpha.
	if _, frags, err := im.ReadWithProvenance(make([]byte, 36), 64); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(frags, wantAlpha) {
		t.Fatalf("tail fragments = %+v, want %+v", frags, wantAlpha)
	}

	// A straddling read merges both sources into the minimal set.
	_, frags, err = im.ReadWithProvenance(make([]byte, 100), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []Fragment{
		{Source: "alpha", Span: Span{Off: 0, Len: 100}},
		{Source: "beta", Span: Span{Off: 0, Len: 64}},
	}
	if !reflect.DeepEqual(frags, want) {
		t.Fatalf("straddling fragments = %+v, want %+v", frags, want)
	}
}

// A failed resolution against a missing dependency must leave nothing
// in the cache; once evidence arrives, the derived block attributes it.
func TestMissingDependencyNotCachedAsEvidence(t *testing.T) {
	im := New()
	mustDeclare(t, im, Segment{
		Span: Span{Off: 200, Len: 100}, Kind: Derived,
		Deps: []Span{{Off: 300, Len: 100}},
		Data: sourceOf(string(fill('d', 100))),
	})

	if _, _, err := im.ReadWithProvenance(make([]byte, 100), 200); !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v, want missing", err)
	}

	if err := im.AddSource("gamma", sourceOf(string(fill('g', 100)))); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 300, Len: 100}, Kind: Original, Source: "gamma",
	})

	_, frags, err := im.ReadWithProvenance(make([]byte, 100), 200)
	if err != nil {
		t.Fatal(err)
	}
	want := []Fragment{{Source: "gamma", Span: Span{Off: 0, Len: 100}}}
	if !reflect.DeepEqual(frags, want) {
		t.Fatalf("fragments = %+v, want %+v", frags, want)
	}
}

func TestReaderAtInterface(t *testing.T) {
	var _ io.ReaderAt = (*Image)(nil)

	im := New()
	if err := im.AddSource("disk0", sourceOf("hello, sparse world")); err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, im, Segment{
		Span: Span{Off: 0, Len: 19}, Kind: Original, Source: "disk0",
	})
	buf := make([]byte, 6)
	if n, err := im.ReadAt(buf, 7); err != nil || n != 6 || string(buf) != "sparse" {
		t.Fatalf("ReadAt = %d, %q, %v", n, buf, err)
	}
}

func TestDeclareValidation(t *testing.T) {
	im := New()
	if err := im.Declare(Segment{Span: Span{Off: 0, Len: 0}, Kind: Missing}); err == nil {
		t.Fatal("empty span accepted")
	}
	if err := im.Declare(Segment{Span: Span{Off: -1, Len: 4}, Kind: Missing}); err == nil {
		t.Fatal("negative offset accepted")
	}
	if err := im.Declare(Segment{Span: Span{Off: 0, Len: 4}, Kind: Original}); err == nil {
		t.Fatal("original without source accepted")
	}
	if err := im.Declare(Segment{Span: Span{Off: 0, Len: 4}, Kind: Derived}); err == nil {
		t.Fatal("derived without dependencies accepted")
	}
	if err := im.Declare(Segment{
		Span: Span{Off: 0, Len: 4}, Kind: Original, Source: "ghost",
	}); err == nil {
		t.Fatal("original with unregistered source accepted")
	}
}
