package netsec

import (
	"errors"
	"testing"
)

func fixedVerifier(identity string, ok bool) Verifier {
	return func(Section) (string, bool) { return identity, ok }
}

func signedAll(n int) []ByteRange {
	return []ByteRange{{Offset: 0, Length: uint32(n)}}
}

// A signed, verified executable section is later overwritten by an
// unsigned section; the overwritten addresses must be reported.
func TestUnsignedOverwriteOfSignedCode(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x1000, Raw: []byte{0xAA, 0xBB, 0xCC, 0xDD}, Exec: true,
			Identity: "fw", Signed: signedAll(4)},
		{ID: 2, Address: 0x1002, Raw: []byte{0xEE, 0xEE}},
	}
	var loads []LoadEvent
	v, err := Audit(sections, "fw", fixedVerifier("fw", true),
		func(e LoadEvent) { loads = append(loads, e) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("expected violation, got nil")
	}
	if v.Address != 0x1002 {
		t.Errorf("violation address = %#x, want 0x1002", v.Address)
	}
	if v.LastWriter != 2 {
		t.Errorf("last writer = %d, want 2", v.LastWriter)
	}
	if v.ExecSection != 1 {
		t.Errorf("exec section = %d, want 1", v.ExecSection)
	}
	if len(loads) != 2 || loads[0].SectionID != 1 || loads[1].SectionID != 2 {
		t.Errorf("loads out of order: %+v", loads)
	}
}

// An RLE pair whose count byte is signed but whose value byte is not must
// expand to untrusted bytes.
func TestRLEOnlyCountSigned(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x2000, Raw: []byte{3, 0x55}, RLE: true, Exec: true,
			Identity: "fw", Signed: []ByteRange{{Offset: 0, Length: 1}}},
	}
	v, err := Audit(sections, "fw", fixedVerifier("fw", true), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("expected violation, got nil")
	}
	if v.Address != 0x2000 {
		t.Errorf("violation address = %#x, want 0x2000", v.Address)
	}
	if v.LastWriter != 1 || v.ExecSection != 1 {
		t.Errorf("writer/exec = %d/%d, want 1/1", v.LastWriter, v.ExecSection)
	}
}

// A later section rewrites the same byte value over signed code. Identical
// content does not make the byte trusted.
func TestOverlapSameValueStillUntrusted(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x3000, Raw: []byte{0x42, 0x42}, Exec: true,
			Identity: "fw", Signed: signedAll(2)},
		{ID: 2, Address: 0x3001, Raw: []byte{0x42}}, // same value, unsigned
	}
	v, err := Audit(sections, "fw", fixedVerifier("fw", true), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("expected violation despite identical byte value")
	}
	if v.Address != 0x3001 {
		t.Errorf("violation address = %#x, want 0x3001", v.Address)
	}
	if v.LastWriter != 2 {
		t.Errorf("last writer = %d, want 2", v.LastWriter)
	}
}

// A non-overlapping image with a fully signed executable section and an
// unsigned data section outside the execute range is accepted.
func TestNonOverlappingValidImage(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x4000, Raw: []byte{1, 2, 3, 4}, Exec: true,
			Identity: "fw", Signed: signedAll(4)},
		{ID: 2, Address: 0x5000, Raw: []byte{5, 6}},
		{ID: 3, Address: 0x6000, Raw: []byte{2, 0x7F}, RLE: true, Exec: true,
			Identity: "fw", Signed: signedAll(2)},
	}
	var loads []LoadEvent
	v, err := Audit(sections, "fw", fixedVerifier("fw", true),
		func(e LoadEvent) { loads = append(loads, e) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Fatalf("unexpected violation: %+v", v)
	}
	if len(loads) != 3 {
		t.Fatalf("got %d load events, want 3", len(loads))
	}
	if loads[2].Length != 2 {
		t.Errorf("RLE section expanded length = %d, want 2", loads[2].Length)
	}
}

// A verified section whose authenticated identity differs from the target
// identity must not be executable.
func TestWrongAuthenticatedIdentity(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x7000, Raw: []byte{0x90}, Exec: true,
			Identity: "fw", Signed: signedAll(1)},
	}
	v, err := Audit(sections, "fw", fixedVerifier("evil", true), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil || v.Address != 0x7000 {
		t.Fatalf("expected violation at 0x7000, got %+v", v)
	}
}

// Signature verification failure makes covered bytes untrusted too.
func TestFailedVerification(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x7100, Raw: []byte{0x90}, Exec: true,
			Identity: "fw", Signed: signedAll(1)},
	}
	v, err := Audit(sections, "fw", fixedVerifier("fw", false), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("expected violation when verifier fails")
	}
}

// Zero-length sections are allowed and contribute no execute range.
func TestZeroLengthExecSection(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x8000, Raw: nil, Exec: true, Identity: "fw"},
		{ID: 2, Address: 0x8000, Raw: nil, RLE: true, Exec: true, Identity: "fw"},
	}
	v, err := Audit(sections, "fw", fixedVerifier("fw", true), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Fatalf("unexpected violation: %+v", v)
	}
}

func TestValidationRejectsBeforeAnyLoad(t *testing.T) {
	bigRLE := make([]byte, 0, 2*((MaxExpansionBytes/255)+1))
	for i := 0; i < (MaxExpansionBytes/255)+1; i++ {
		bigRLE = append(bigRLE, 255, 0x00)
	}
	manySections := make([]Section, MaxSections+1)
	for i := range manySections {
		manySections[i] = Section{ID: uint16(i), Address: 0x1000}
	}

	cases := []struct {
		name     string
		sections []Section
		want     error
	}{
		{"too many sections", manySections, ErrTooManySections},
		{"duplicate ID", []Section{
			{ID: 1, Address: 0x1000}, {ID: 1, Address: 0x2000},
		}, ErrDuplicateID},
		{"RLE odd length", []Section{
			{ID: 1, Address: 0x1000, Raw: []byte{1, 2, 3}, RLE: true},
		}, ErrRLEOddLength},
		{"RLE zero count", []Section{
			{ID: 1, Address: 0x1000, Raw: []byte{0, 0xAA}, RLE: true},
		}, ErrRLEBadCount},
		{"expansion limit", []Section{
			{ID: 1, Address: 0x1000, Raw: bigRLE, RLE: true},
		}, ErrExpansionLimit},
		{"address overflow", []Section{
			{ID: 1, Address: 0xFFFFFFFF, Raw: []byte{1, 2}},
		}, ErrAddressOverflow},
		{"RLE address overflow", []Section{
			{ID: 1, Address: 0xFFFFFFFF, Raw: []byte{2, 0xAA}, RLE: true},
		}, ErrAddressOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			_, err := Audit(tc.sections, "fw", fixedVerifier("fw", true),
				func(LoadEvent) { called = true })
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if called {
				t.Fatal("load callback ran despite validation failure")
			}
		})
	}
}

// Trust must come from coverage plus verification, not from a byte at the
// same address matching what a signed section would have written.
func TestFirstViolationIsLowestAddress(t *testing.T) {
	sections := []Section{
		{ID: 1, Address: 0x9000, Raw: []byte{1, 2, 3, 4}, Exec: true,
			Identity: "fw", Signed: signedAll(4)},
		{ID: 2, Address: 0x9001, Raw: []byte{9, 9, 9}}, // overwrites 0x9001..0x9003
	}
	v, err := Audit(sections, "fw", fixedVerifier("fw", true), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil || v.Address != 0x9001 {
		t.Fatalf("expected first violation at 0x9001, got %+v", v)
	}
}
