package netsec

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
)

func bigu(x uint64) *big.Int { return new(big.Int).SetUint64(x) }

// TestExactArithmeticNoOverflow proves addresses beyond 2^64 are handled with
// exact integers instead of silently wrapping to a small boot address.
func TestExactArithmeticNoOverflow(t *testing.T) {
	imageSize := uint64(1 << 20)
	c, err := New(imageSize)
	if err != nil {
		t.Fatal(err)
	}

	desc := Descriptor{
		Name:   "huge",
		Base:   bigu(math.MaxUint64),
		Offset: big.NewInt(4),
		Scale:  big.NewInt(1 << 40),
		Length: 16,
	}
	if _, err := c.Compile([]Descriptor{desc}, nil); err == nil {
		t.Fatal("strict mode must reject an exact start beyond the image")
	}

	mc, err := New(imageSize, WithModularDecode())
	if err != nil {
		t.Fatal(err)
	}
	report, err := mc.Compile([]Descriptor{desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := report.Results[0]
	wantStart := new(big.Int).Add(bigu(math.MaxUint64), new(big.Int).Lsh(big.NewInt(4), 40))
	if res.Start.Cmp(wantStart) != 0 {
		t.Fatalf("exact start = %s, want %s", res.Start, wantStart)
	}
	if !res.RepresentationWrap {
		t.Fatal("start beyond 2^64 must flag representation wrap")
	}
}

// TestNegativeOffsetUnderflow exercises signed offsets driving the start
// below zero, including the modular-decode underflow path.
func TestNegativeOffsetUnderflow(t *testing.T) {
	c, _ := New(16)
	desc := Descriptor{
		Name:   "under",
		Base:   bigu(4),
		Offset: big.NewInt(-3),
		Scale:  bigu(2),
		Length: 4,
	}
	if _, err := c.Compile([]Descriptor{desc}, nil); err == nil {
		t.Fatal("strict mode must reject a negative start")
	}

	mc, _ := New(16, WithModularDecode())
	report, err := mc.Compile([]Descriptor{desc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Results[0].Underflow {
		t.Fatal("negative exact start must flag underflow")
	}
	// start = 4 - 6 = -2; physical start = -2 mod 16 = 14, then wrap.
	if len(report.WriteSet) != 2 ||
		report.WriteSet[0].Start != 0 || report.WriteSet[0].End != 2 ||
		report.WriteSet[1].Start != 14 || report.WriteSet[1].End != 16 {
		t.Fatalf("underflow split mismatch: %+v", report.WriteSet)
	}
}

// TestAliasMasksBootWrite is the headline scenario: two descriptors alias the
// same physical range and the one touching the boot region is only visible
// through the original-description chain on the violation.
func TestAliasMasksBootWrite(t *testing.T) {
	const imageSize = uint64(256)
	descs := []Descriptor{
		{Name: "payload", Base: bigu(128), Offset: big.NewInt(0), Scale: bigu(1), Length: 64},
		{Name: "shadow", Base: bigu(128), Offset: big.NewInt(2), Scale: bigu(0), Length: 64},
	}
	protected := []Region{{Name: "boot", Start: 0, End: 32}}

	strict, _ := New(imageSize)
	if _, err := strict.Compile(descs, protected); err != nil {
		t.Fatalf("in-image descriptors must compile in strict mode: %v", err)
	}

	// Now make "shadow" wrap around and land on the boot region while
	// "payload" stays in the middle of the image.
	descs[1].Base = bigu(250)
	descs[1].Length = 20
	mc, _ := New(imageSize, WithModularDecode())
	report, err := mc.Compile(descs, protected)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Results[1].DomainWrap {
		t.Fatal("shadow descriptor must be flagged as domain wrapping")
	}
	if len(report.Violations) != 1 || report.Violations[0].Region != "boot" ||
		report.Violations[0].Start != 0 || report.Violations[0].End != 14 {
		t.Fatalf("boot violation mismatch: %+v", report.Violations)
	}
	chain := report.Violations[0].Descriptors
	if len(chain) != 1 || chain[0] != "shadow" {
		t.Fatalf("violation chain must name the original descriptor: %v", chain)
	}
}

// TestAliasingChainOnOverlap makes two descriptors physically overlap the
// same protected byte and checks the chain names both.
func TestAliasingChainOnOverlap(t *testing.T) {
	const imageSize = uint64(64)
	descs := []Descriptor{
		{Name: "a", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 8},
		{Name: "b", Base: bigu(4), Offset: big.NewInt(0), Scale: bigu(1), Length: 8},
	}
	protected := []Region{{Name: "boot", Start: 0, End: 16}}
	c, _ := New(imageSize)
	report, err := c.Compile(descs, protected)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Start == 4 && v.End == 8 && len(v.Descriptors) == 2 &&
			v.Descriptors[0] == "a" && v.Descriptors[1] == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("overlapping alias chain missing: %+v", report.Violations)
	}
}

func TestValidationAndAggregateErrors(t *testing.T) {
	c, _ := New(16)
	_, err := c.Compile([]Descriptor{
		{Name: "x", Base: bigu(0), Offset: big.NewInt(1), Scale: bigu(1), Length: 0},
	}, []Region{{Name: "bad", Start: 8, End: 8}})
	var agg CompileErrors
	if !errors.As(err, &agg) || len(agg) != 2 {
		t.Fatalf("want 2 aggregated compile errors, got %v", err)
	}

	if _, err := New(3, WithModularDecode(), withRepresentationBits(4)); err == nil {
		t.Fatal("non-power-of-two D must be rejected in modular mode")
	}
	if _, err := New(0); err == nil {
		t.Fatal("zero image size must be rejected")
	}
}

// TestWriteSetCanonical verifies normalization output is sorted, disjoint and
// fully covers the union of the inputs.
func TestWriteSetCanonical(t *testing.T) {
	c, _ := New(32)
	descs := []Descriptor{
		{Name: "p", Base: bigu(10), Offset: big.NewInt(0), Scale: bigu(1), Length: 10},
		{Name: "q", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 5},
		{Name: "r", Base: bigu(20), Offset: big.NewInt(0), Scale: bigu(1), Length: 5},
	}
	report, err := c.Compile(descs, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Segment{
		{Start: 0, End: 5, Descriptors: []string{"q"}},
		{Start: 10, End: 20, Descriptors: []string{"p"}},
		{Start: 20, End: 25, Descriptors: []string{"r"}},
	}
	if len(report.WriteSet) != len(want) {
		t.Fatalf("write set = %+v, want %+v", report.WriteSet, want)
	}
	for i := range want {
		got := report.WriteSet[i]
		if got.Start != want[i].Start || got.End != want[i].End || !equalChain(got.Descriptors, want[i].Descriptors) {
			t.Fatalf("segment %d = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestErrorStringsAndOptionBranches(t *testing.T) {
	// CompileError without a descriptor name.
	e := &CompileError{Op: "protect", Reason: "bad range"}
	if e.Error() != "netsec: protect: bad range" {
		t.Fatalf("unexpected error text: %q", e.Error())
	}

	// Representation width out of range.
	if _, err := New(8, withRepresentationBits(0)); err == nil {
		t.Fatal("zero bits must be rejected")
	}
	if _, err := New(8, withRepresentationBits(65)); err == nil {
		t.Fatal("bits above 64 must be rejected")
	}

	// Malformed descriptors exercise every compileOne validation branch.
	c, _ := New(16)
	cases := []Descriptor{
		{Name: "nilbase", Base: nil, Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
		{Name: "negbase", Base: big.NewInt(-1), Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
		{Name: "negscale", Base: bigu(0), Offset: big.NewInt(0), Scale: big.NewInt(-1), Length: 1},
		{Name: "zerolen", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 0},
		{Name: "neglen", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: -2},
	}
	for _, desc := range cases {
		if _, err := c.Compile([]Descriptor{desc}, nil); err == nil {
			t.Fatalf("descriptor %q must be rejected", desc.Name)
		}
	}

	// Auto-generated names and duplicate-name rejection.
	_, err := c.Compile([]Descriptor{
		{Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
		{Name: "dup", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
		{Name: "dup", Base: bigu(1), Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
	}, nil)
	if err == nil {
		t.Fatal("duplicate names must be rejected")
	}

	// Protected region extending past the image.
	_, err = c.Compile(nil, []Region{{Name: "oob", Start: 0, End: 17}})
	if err == nil {
		t.Fatal("out-of-image protected region must be rejected")
	}
}

func TestEmptyCompileAndUnprotected(t *testing.T) {
	c, _ := New(8)
	report, err := c.Compile(nil, nil)
	if err != nil || len(report.WriteSet) != 0 || len(report.Violations) != 0 {
		t.Fatalf("empty input must yield an empty report: %+v err=%v", report, err)
	}

	// A fully covered image with no protected regions produces one segment.
	descs := []Descriptor{
		{Name: "x", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 8},
	}
	report, err = c.Compile(descs, nil)
	if err != nil || len(report.WriteSet) != 1 || len(report.Violations) != 0 {
		t.Fatalf("unexpected report: %+v err=%v", report, err)
	}
}

func TestAggregateErrorTextAndViolationMerge(t *testing.T) {
	// Two structural errors at once exercises CompileErrors.Error joining.
	c, _ := New(16)
	_, err := c.Compile(
		[]Descriptor{
			{Name: "bad1", Base: big.NewInt(-1), Offset: big.NewInt(0), Scale: bigu(1), Length: 1},
			{Name: "bad2", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 0},
		},
		nil,
	)
	var agg CompileErrors
	if !errors.As(err, &agg) || len(agg) != 2 {
		t.Fatalf("want 2 aggregated errors, got %v", err)
	}
	text := err.Error()
	if !strings.Contains(text, "bad1") || !strings.Contains(text, "bad2") ||
		!strings.Contains(text, "; ") {
		t.Fatalf("aggregate error text must name both descriptors: %q", text)
	}

	// Two adjacent protected regions with one uniform descriptor chain must
	// be merged into a single contiguous violation.
	protected := []Region{
		{Name: "lo", Start: 0, End: 4},
		{Name: "hi", Start: 4, End: 8},
	}
	report, err := c.Compile([]Descriptor{
		{Name: "z", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 8},
	}, protected)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 2 {
		t.Fatalf("distinct region names must stay separate: %+v", report.Violations)
	}

	// Same region name across the boundary forces the adjacent-merge path.
	mergedProtected := []Region{
		{Name: "boot", Start: 0, End: 4},
		{Name: "boot", Start: 4, End: 8},
	}
	report, err = c.Compile([]Descriptor{
		{Name: "z", Base: bigu(0), Offset: big.NewInt(0), Scale: bigu(1), Length: 8},
	}, mergedProtected)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 1 || report.Violations[0].Start != 0 ||
		report.Violations[0].End != 8 {
		t.Fatalf("adjacent same-chain violations must merge: %+v", report.Violations)
	}
}
