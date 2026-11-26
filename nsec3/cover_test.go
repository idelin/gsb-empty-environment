package nsec3

import (
	"errors"
	"testing"
)

func hb(b ...byte) []byte { return b }

func TestCoversHashOrdinaryInterval(t *testing.T) {
	owner := hb(0x10)
	next := hb(0x90)
	cases := []struct {
		x    byte
		want bool
	}{
		{0x0F, false}, // below owner
		{0x10, false}, // equal to owner: exact match, not cover
		{0x11, true},
		{0x50, true},
		{0x8F, true},
		{0x90, false}, // equal to next
		{0x91, false}, // above next
	}
	for _, c := range cases {
		if got := CoversHash(owner, next, hb(c.x)); got != c.want {
			t.Errorf("ordinary cover(%#x) = %v, want %v", c.x, got, c.want)
		}
	}
}

func TestCoversHashWrappingInterval(t *testing.T) {
	// owner > next: interval wraps through the ring zero.
	owner := hb(0xC0)
	next := hb(0x20)
	cases := []struct {
		x    byte
		want bool
	}{
		{0x00, true}, // below next, on the wrap branch
		{0x1F, true},
		{0x20, false}, // equal next
		{0x21, false}, // gap between next and owner
		{0x80, false},
		{0xBF, false},
		{0xC0, false}, // equal owner
		{0xC1, true},  // above owner, wrap branch
		{0xFF, true},
	}
	for _, c := range cases {
		if got := CoversHash(owner, next, hb(c.x)); got != c.want {
			t.Errorf("wrapping cover(%#x) = %v, want %v", c.x, got, c.want)
		}
	}
}

func TestCoversHashWholeRing(t *testing.T) {
	// A single-record chain (owner == next) covers every other point.
	owner := hb(0x42)
	if CoversHash(owner, owner, owner) {
		t.Error("single-record chain must not cover its own owner")
	}
	if !CoversHash(owner, owner, hb(0x00)) {
		t.Error("single-record chain must cover 0x00")
	}
	if !CoversHash(owner, owner, hb(0xFF)) {
		t.Error("single-record chain must cover 0xFF")
	}
	// Multi-byte values share the same comparison semantics.
	lo := hb(0x00, 0x00, 0x05)
	hi := hb(0x00, 0x00, 0x09)
	if !CoversHash(lo, hi, hb(0x00, 0x00, 0x07)) {
		t.Error("multi-byte ordinary interval failed")
	}
	if CoversHash(lo, hi, hb(0x00, 0x00, 0x09)) {
		t.Error("multi-byte endpoint treated as cover")
	}
}

func TestCoversHashLengthMismatch(t *testing.T) {
	if CoversHash(hb(1, 2), hb(1, 2), hb(1)) {
		t.Error("length mismatch must never cover")
	}
}

func TestTypeBitmapWindows(t *testing.T) {
	// Types spread over window 0 and window 1, exercising octet growth.
	types := []uint16{TypeNS, TypeSOA, TypeCNAME, TypeDS, 256 + 13, 300}
	wire := EncodeTypeBitmap(types)
	got, err := ParseTypeBitmap(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, ty := range types {
		if !got[ty] {
			t.Errorf("type %d lost in round trip", ty)
		}
	}
	if got[TypeA] {
		t.Error("unexpected type bit set")
	}
	// Re-encoding must be canonical and stable (sorted windows).
	again := EncodeTypeBitmap(types)
	if string(again) != string(wire) {
		t.Error("bitmap encoding not stable")
	}
}

func TestParseTypeBitmapErrors(t *testing.T) {
	bad := [][]byte{
		{0, 1},           // window/length header without bitmap bytes
		{0, 0},           // zero-length bitmap
		{0, 33, 0, 0, 0}, // length > 32
		{0, 2, 0},        // truncated bitmap
	}
	for i, b := range bad {
		if _, err := ParseTypeBitmap(b); err == nil {
			t.Errorf("bad bitmap %d accepted", i)
		} else if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("bad bitmap %d: %v, want ErrInvalidRecord", i, err)
		}
	}
}

func TestRecordRDataRoundTrip(t *testing.T) {
	original := &Record{
		Algorithm:  HashSHA1,
		Flags:      optOutFlag,
		Iterations: 7,
		Salt:       []byte{0xde, 0xad},
		NextHash:   mustHash(t, "next.example."),
		OwnerHash:  mustHash(t, "owner.example."),
		Types:      typeSet([]uint16{TypeNS, TypeDS, TypeSOA}),
	}
	wire, err := original.MarshalRData()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRData(wire)
	if err != nil {
		t.Fatalf("ParseRData: %v", err)
	}
	if parsed.Algorithm != original.Algorithm || parsed.Flags != original.Flags ||
		parsed.Iterations != original.Iterations || !parsed.OptOut() ||
		string(parsed.Salt) != string(original.Salt) ||
		string(parsed.NextHash) != string(original.NextHash) {
		t.Fatal("RDATA round trip mismatch")
	}
	for _, ty := range []uint16{TypeNS, TypeDS, TypeSOA} {
		if !parsed.HasType(ty) {
			t.Errorf("type %d missing after parse", ty)
		}
	}
	if parsed.HasType(TypeCNAME) {
		t.Error("CNAME bit unexpectedly present")
	}
}

func TestParseRDataRejectsGarbage(t *testing.T) {
	if _, err := ParseRData(nil); !errors.Is(err, ErrInvalidRecord) {
		t.Errorf("nil rdata: %v", err)
	}
	// Unknown algorithm.
	bad := &Record{Algorithm: 2, Iterations: 0, NextHash: hb(1), Types: map[uint16]bool{}}
	if err := bad.Validate(); !errors.Is(err, ErrUnknownAlgorithm) {
		t.Errorf("unknown alg: %v", err)
	}
	// Iteration policy.
	bad = &Record{Algorithm: HashSHA1, Iterations: MaxIterations + 1, NextHash: hb(1), Types: map[uint16]bool{}}
	if err := bad.Validate(); !errors.Is(err, ErrIterationsTooLarge) {
		t.Errorf("iterations: %v", err)
	}
}

func TestOwnerLabelRoundTrip(t *testing.T) {
	h := mustHash(t, "a.example.")
	label := OwnerLabel(h)
	back, err := OwnerHashFromLabel(label)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(h) {
		t.Fatal("owner label round trip failed")
	}
	if _, err := OwnerHashFromLabel("not-base32hex!"); err == nil {
		t.Error("invalid label accepted")
	}
}

func TestParseRDataTruncation(t *testing.T) {
	base := &Record{
		Algorithm: HashSHA1, Flags: 0, Iterations: 3, Salt: []byte{0xaa},
		NextHash: hb(1, 2, 3, 4), Types: typeSet([]uint16{TypeA, TypeDS}),
	}
	wire, err := base.MarshalRData()
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{1, 4, 5, 6, 7, len(wire) - 1} {
		if _, err := ParseRData(wire[:cut]); err == nil {
			t.Errorf("truncated RDATA at %d accepted", cut)
		}
	}
	if _, err := ParseRData(append(wire, 9, 9)); err == nil {
		t.Error("trailing garbage bitmap accepted")
	}
}
