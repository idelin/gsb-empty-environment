package gdict

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCollationCycleCertificate(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{
			{Name: "p1", Version: "v1", Entries: []Entry{{0, a}, {1, b}}},
			{Name: "p2", Version: "v2", Entries: []Entry{{0, b}, {1, a}}},
		},
		Preorders: []Preorder{
			{Version: "v1", Ranks: map[Key]int64{a: 1, b: 2}},
			{Version: "v2", Ranks: map[Key]int64{b: 1, a: 2}},
		},
		Space: CodeSpace{Limit: 100},
	}
	_, err := Solve(cfg)
	if err == nil {
		t.Fatal("expected conflict")
	}
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Observations) < 2 {
		t.Fatalf("certificate needs two observations: %+v", ie.Cert)
	}
	if !strings.Contains(ie.Error(), "v1") || !strings.Contains(ie.Error(), "v2") {
		t.Fatalf("certificate does not name both versions: %v", err)
	}
}

func TestFixedCodeConflictCertificate(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {1, b}}}},
		Preorders:  []Preorder{{Version: "v", Ranks: map[Key]int64{a: 1, b: 2}}},
		Space:      CodeSpace{Limit: 100, Fixed: []Fixed{{Code: 50, Key: a}, {Code: 40, Key: b}}},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Fixed) < 2 {
		t.Fatalf("want two fixed constraints: %+v", ie.Cert)
	}
}

func TestAliasStrictConflict(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}}}},
		Aliases:    []Alias{{Name: "ab", Members: []Key{a, b}}},
		Preorders:  []Preorder{{Version: "v", Ranks: map[Key]int64{a: 1, b: 2}}},
		Space:      CodeSpace{Limit: 100},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
}

func TestVerifyCatchesTamper(t *testing.T) {
	a, b := Text("a"), Text("b")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {1, b}}}},
		Preorders:  []Preorder{{Version: "v", Ranks: map[Key]int64{a: 1, b: 2}}},
		Space:      CodeSpace{Limit: 100},
	}
	plan, err := Solve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan.Entries[1].NewCode++
	if err := Verify(cfg, plan); err == nil {
		t.Fatal("tampered plan verified")
	}
}

func TestFixedTransitiveOrderConflict(t *testing.T) {
	a, b, c := Text("a"), Text("b"), Text("c")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {1, b}, {2, c}}}},
		Preorders:  []Preorder{mkpre("v", a, b, c)},
		Space:      CodeSpace{Limit: 100, Fixed: []Fixed{{Code: 50, Key: a}, {Code: 40, Key: c}}},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Fixed) < 2 {
		t.Fatalf("want fixed constraints, got %+v", ie.Cert)
	}
}

func TestFixedCodeInHoleCertificate(t *testing.T) {
	a := Text("a")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}}}},
		Preorders:  []Preorder{mkpre("v", a)},
		Space:      CodeSpace{Limit: 100, Holes: []Interval{{Lo: 5, Hi: 9}}, Fixed: []Fixed{{Code: 7, Key: a}}},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Fixed) != 1 || len(ie.Cert.Observations) == 0 {
		t.Fatalf("want fixed + hole observation: %+v", ie.Cert)
	}
}

func TestCodeSpaceExhaustionCertificate(t *testing.T) {
	a, b, c := Text("a"), Text("b"), Text("c")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {1, b}, {2, c}}}},
		Preorders:  []Preorder{mkpre("v", a, b, c)},
		Space:      CodeSpace{Limit: 2},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Observations) < 2 && len(ie.Cert.Fixed) < 2 {
		t.Fatalf("want at least two observations: %+v", ie.Cert)
	}
}

func TestTooManyConflictClasses(t *testing.T) {
	var keys []Key
	ranks := map[Key]int64{}
	for i := 0; i < MaxConflictClasses+1; i++ {
		a, b := Text(fmt.Sprintf("a%d", i)), Text(fmt.Sprintf("b%d", i))
		keys = append(keys, a, b)
		ranks[a] = int64(i)
		ranks[b] = int64(i)
	}
	var entries []Entry
	for i, k := range keys {
		entries = append(entries, Entry{OldCode: uint64(i), Key: k})
	}
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: entries}},
		Preorders:  []Preorder{{Version: "v", Ranks: ranks}},
		Space:      CodeSpace{Limit: 100},
	}
	if _, err := Solve(cfg); !errors.Is(err, ErrTooManyConflictClasses) {
		t.Fatalf("want ErrTooManyConflictClasses, got %v", err)
	}
}

func TestCutSplittingRepeatedKeyCertificate(t *testing.T) {
	a := Text("a")
	cfg := Config{
		Partitions: []Partition{{Name: "p", Version: "v", Entries: []Entry{{0, a}, {10, a}}, Cuts: []Cut{{Name: "split", OldCode: 5, Bound: LT}}}},
		Preorders:  []Preorder{mkpre("v", a)},
		Space:      CodeSpace{Limit: 100},
	}
	_, err := Solve(cfg)
	var ie *IncompatibleError
	if !errors.As(err, &ie) {
		t.Fatalf("want IncompatibleError, got %T %v", err, err)
	}
	if len(ie.Cert.Observations) < 2 {
		t.Fatalf("want two range observations: %+v", ie.Cert)
	}
}
