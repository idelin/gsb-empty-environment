package netsec

import (
	"bytes"
	"errors"
	"testing"
)

const maxU64 = ^uint64(0)

func fill(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 0xAB
	}
	return b
}

// testSegments returns a fixed, non-overlapping layout:
//
//	0: writable non-executable target segment at base 0x1000 (len 16)
//	1: writable non-executable second target at base 0x2000 (len 16)
//	2: read-only symbol segment at base 0x8000 (len 16)
//	3: write-only segment (no read) at base 0x9000 (len 16)
//	4: writable+executable segment at base 0xA000 (len 16)
//	5: low-address read-only symbol segment at base 0x3000 (len 16)
func testSegments() []Segment {
	return []Segment{
		{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
		{Base: 0x2000, Data: fill(16), Perms: PermRead | PermWrite},
		{Base: 0x8000, Data: fill(16), Perms: PermRead},
		{Base: 0x9000, Data: fill(16), Perms: PermWrite},
		{Base: 0xA000, Data: fill(16), Perms: PermRead | PermWrite | PermExecute},
		{Base: 0x3000, Data: fill(16), Perms: PermRead},
	}
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func assertBytes(t *testing.T, got []byte, offset uint64, want uint64, width uint8) {
	t.Helper()
	expected := make([]byte, width)
	if width == 8 {
		putUint64(expected, want)
	} else {
		putUint32(expected, want)
	}
	if !bytes.Equal(got[offset:offset+uint64(width)], expected) {
		t.Fatalf("bytes at %d = % x, want % x", offset,
			got[offset:offset+uint64(width)], expected)
	}
}

// A legal negative addend must produce the exact mathematical integer without
// any truncation, exercising the signed integer arithmetic path.
func TestApply_LegalNegativeAddend_Width8(t *testing.T) {
	l, err := NewLoader(testSegments())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}

	// symbol address 0x8008, addend -0x100 => 0x7F08.
	r := Relocation{
		TargetID: 0, TargetOffset: 0, Width: 8,
		SymbolID: 2, SymbolOffset: 8, Addend: -0x100,
	}
	if err := l.Apply([]Relocation{r}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := l.Segments()
	assertBytes(t, got[0].Data, 0, 0x7F08, 8)
}

func TestApply_LegalPositiveAddend_Width4(t *testing.T) {
	l, _ := NewLoader(testSegments())

	// symbol address 0x8000, addend 0x41 => 0x8041, fits in 4 bytes.
	r := Relocation{
		TargetID: 1, TargetOffset: 4, Width: 4,
		SymbolID: 2, SymbolOffset: 0, Addend: 0x41,
	}
	if err := l.Apply([]Relocation{r}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := l.Segments()
	assertBytes(t, got[1].Data, 4, 0x8041, 4)
	// Surrounding bytes are untouched.
	if got[1].Data[3] != 0xAB || got[1].Data[8] != 0xAB {
		t.Fatalf("adjacent bytes were modified: % x", got[1].Data)
	}
}

func TestApply_RejectsTruncatedWritePastSegment(t *testing.T) {
	cases := []struct {
		name   string
		offset uint64
		width  uint8
	}{
		{"width8 one byte past end", 9, 8},
		{"width4 one byte past end", 13, 4},
		{"offset equals length", 16, 4},
		{"offset beyond length", 100, 8},
	}
	base := testSegments()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := NewLoader(base)
			r := Relocation{
				TargetID: 0, TargetOffset: tc.offset, Width: tc.width,
				SymbolID: 2, SymbolOffset: 0, Addend: 0,
			}
			wantErr(t, l.Apply([]Relocation{r}), ErrTargetOutOfRange)
		})
	}
}

// A value that needs 8 bytes must not be silently truncated into a 4-byte slot.
func TestApply_RejectsValueTooWideForWidth4(t *testing.T) {
	l, _ := NewLoader(testSegments())
	r := Relocation{
		TargetID: 0, TargetOffset: 0, Width: 4,
		// symbol 0x3000 + addend 0xFFFFFFF0 = 0x10002FFF0, exceeds uint32.
		SymbolID: 5, SymbolOffset: 0, Addend: int64(0xFFFFFFF0),
	}
	wantErr(t, l.Apply([]Relocation{r}), ErrValueTooWide)
}

func TestApply_RejectsAddressWraparound(t *testing.T) {
	t.Run("symbol base+offset overflow", func(t *testing.T) {
		segs := []Segment{
			{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
			// Highest representable span: [maxU64-1, maxU64). Any positive
			// addend pushes the value past uint64.
			{Base: maxU64 - 1, Data: fill(1), Perms: PermRead},
		}
		l, err := NewLoader(segs)
		if err != nil {
			t.Fatalf("NewLoader: %v", err)
		}
		r := Relocation{
			TargetID: 0, TargetOffset: 0, Width: 8,
			SymbolID: 1, SymbolOffset: 0, Addend: 2,
		}
		wantErr(t, l.Apply([]Relocation{r}), ErrAddressOverflow)
	})

	t.Run("positive addend overflow", func(t *testing.T) {
		segs := []Segment{
			{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
			{Base: maxU64 - 1, Data: fill(1), Perms: PermRead},
		}
		l, err := NewLoader(segs)
		if err != nil {
			t.Fatalf("NewLoader: %v", err)
		}
		r := Relocation{
			TargetID: 0, TargetOffset: 0, Width: 8,
			SymbolID: 1, SymbolOffset: 0, Addend: 10,
		}
		wantErr(t, l.Apply([]Relocation{r}), ErrAddressOverflow)
	})

	t.Run("negative addend underflow below zero", func(t *testing.T) {
		segs := []Segment{
			{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
			{Base: 0x10, Data: fill(4), Perms: PermRead},
		}
		l, _ := NewLoader(segs)
		r := Relocation{
			TargetID: 0, TargetOffset: 0, Width: 8,
			SymbolID: 1, SymbolOffset: 0, Addend: -0x100,
		}
		wantErr(t, l.Apply([]Relocation{r}), ErrAddressOverflow)
	})

	t.Run("segment base+length overflow at construction", func(t *testing.T) {
		segs := []Segment{
			{Base: maxU64 - 2, Data: fill(10), Perms: PermRead},
		}
		if _, err := NewLoader(segs); !errors.Is(err, ErrAddressOverflow) {
			t.Fatalf("expected ErrAddressOverflow, got %v", err)
		}
	})
}

func TestApply_RejectsOverlappingRelocations(t *testing.T) {
	// Two entries whose [offset, offset+width) ranges share a byte.
	overlap := []Relocation{
		{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 2, SymbolOffset: 0},
		{TargetID: 0, TargetOffset: 4, Width: 4, SymbolID: 2, SymbolOffset: 0},
	}
	t.Run("intersecting same segment", func(t *testing.T) {
		l, _ := NewLoader(testSegments())
		wantErr(t, l.Apply(overlap), ErrOverlappingRelocations)
	})

	t.Run("exact duplicate", func(t *testing.T) {
		l, _ := NewLoader(testSegments())
		r := Relocation{TargetID: 0, TargetOffset: 0, Width: 4, SymbolID: 2, SymbolOffset: 0}
		wantErr(t, l.Apply([]Relocation{r, r}), ErrOverlappingRelocations)
	})

	t.Run("touching ranges are allowed", func(t *testing.T) {
		l, _ := NewLoader(testSegments())
		relocs := []Relocation{
			{TargetID: 0, TargetOffset: 0, Width: 4, SymbolID: 2, SymbolOffset: 0},
			{TargetID: 0, TargetOffset: 4, Width: 4, SymbolID: 2, SymbolOffset: 0},
		}
		if err := l.Apply(relocs); err != nil {
			t.Fatalf("adjacent non-overlapping entries rejected: %v", err)
		}
	})

	t.Run("overlap across segments via absolute address", func(t *testing.T) {
		// Two distinct target segments mapped to the same absolute range must
		// be rejected as intersecting even though their indices differ.
		segs := []Segment{
			{Base: 0x4000, Data: fill(16), Perms: PermRead | PermWrite},
			{Base: 0x4000, Data: fill(16), Perms: PermRead | PermWrite},
			{Base: 0x8000, Data: fill(16), Perms: PermRead},
		}
		if _, err := NewLoader(segs); !errors.Is(err, ErrOverlappingSegments) {
			t.Fatalf("expected overlapping segments error, got %v", err)
		}
	})
}

func TestApply_RejectsPermissionAndIndexViolations(t *testing.T) {
	cases := []struct {
		name string
		rel  Relocation
		want error
	}{
		{
			name: "W+X target rejected",
			rel:  Relocation{TargetID: 4, TargetOffset: 0, Width: 8, SymbolID: 2},
			want: ErrTargetExecutable,
		},
		{
			name: "read-only target rejected",
			rel:  Relocation{TargetID: 2, TargetOffset: 0, Width: 8, SymbolID: 2},
			want: ErrTargetNotWritable,
		},
		{
			name: "non-readable symbol rejected",
			rel:  Relocation{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 3},
			want: ErrSymbolNotReadable,
		},
		{
			name: "symbol offset out of range",
			rel:  Relocation{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 2, SymbolOffset: 16},
			want: ErrSymbolOutOfRange,
		},
		{
			name: "invalid target id",
			rel:  Relocation{TargetID: 99, TargetOffset: 0, Width: 8, SymbolID: 2},
			want: ErrInvalidTargetIndex,
		},
		{
			name: "invalid symbol id",
			rel:  Relocation{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 99},
			want: ErrInvalidSymbolIndex,
		},
		{
			name: "invalid width",
			rel:  Relocation{TargetID: 0, TargetOffset: 0, Width: 2, SymbolID: 2},
			want: ErrInvalidWidth,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := NewLoader(testSegments())
			wantErr(t, l.Apply([]Relocation{tc.rel}), tc.want)
		})
	}
}

// A write-only target (no read permission) is still a valid relocation target;
// the spec only forbids execution and requires writability.
func TestApply_AllowsWriteOnlyTarget(t *testing.T) {
	l, _ := NewLoader(testSegments())
	r := Relocation{TargetID: 3, TargetOffset: 0, Width: 8, SymbolID: 2, SymbolOffset: 0}
	if err := l.Apply([]Relocation{r}); err != nil {
		t.Fatalf("write-only target should be allowed: %v", err)
	}
}

// A failed batch must not change a single byte, even when an earlier entry in
// the same batch was individually valid.
func TestApply_AtomicOnFailure(t *testing.T) {
	original := testSegments()
	l, _ := NewLoader(original)
	before := l.Segments()

	badBatch := []Relocation{
		{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 2, SymbolOffset: 0},
		{TargetID: 0, TargetOffset: 4, Width: 8, SymbolID: 2, SymbolOffset: 0}, // overlaps entry 0
	}
	if err := l.Apply(badBatch); !errors.Is(err, ErrOverlappingRelocations) {
		t.Fatalf("expected overlap error, got %v", err)
	}

	after := l.Segments()
	for i := range before {
		if !bytes.Equal(before[i].Data, after[i].Data) {
			t.Fatalf("segment %d changed after failed Apply", i)
		}
	}

	// The caller's original backing slices must be untouched too.
	for i := range original {
		if !bytes.Equal(original[i].Data, fill(len(original[i].Data))) {
			t.Fatalf("caller segment %d was mutated", i)
		}
	}
}

func TestNewLoader_RejectsOverlappingSegments(t *testing.T) {
	segs := []Segment{
		{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
		{Base: 0x100F, Data: fill(8), Perms: PermRead},
	}
	if _, err := NewLoader(segs); !errors.Is(err, ErrOverlappingSegments) {
		t.Fatalf("expected ErrOverlappingSegments, got %v", err)
	}

	// Touching half-open ranges are permitted.
	touching := []Segment{
		{Base: 0x1000, Data: fill(16), Perms: PermRead | PermWrite},
		{Base: 0x1010, Data: fill(8), Perms: PermRead},
	}
	if _, err := NewLoader(touching); err != nil {
		t.Fatalf("touching ranges should be allowed: %v", err)
	}
}

func TestSealTargets_ClearsWriteOnTargetSegments(t *testing.T) {
	l, _ := NewLoader(testSegments())

	if err := l.SealTargets(); !errors.Is(err, ErrNotApplied) {
		t.Fatalf("expected ErrNotApplied, got %v", err)
	}

	r := Relocation{TargetID: 0, TargetOffset: 0, Width: 8, SymbolID: 2, SymbolOffset: 0}
	if err := l.Apply([]Relocation{r}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := l.SealTargets(); err != nil {
		t.Fatalf("SealTargets: %v", err)
	}

	sealed := l.Segments()
	if sealed[0].Writable() {
		t.Fatalf("target segment should no longer be writable")
	}
	if !sealed[0].Readable() {
		t.Fatalf("read permission should be preserved")
	}
	// Only the targeted segment is sealed.
	if !sealed[1].Writable() {
		t.Fatalf("non-target segment must keep its write permission")
	}
	if sealed[4].Executable() != true {
		t.Fatalf("execute permission must be untouched")
	}

	// Sealing is one-shot per successful Apply.
	if err := l.SealTargets(); !errors.Is(err, ErrNotApplied) {
		t.Fatalf("expected ErrNotApplied on second seal, got %v", err)
	}
}

func TestNewLoader_EmptySegmentsNeverOverlap(t *testing.T) {
	// Zero-length segments occupy no address space, so identical bases are
	// permitted and cannot be used as a symbol or target anyway.
	segs := []Segment{
		{Base: 0x5000, Data: nil, Perms: PermRead | PermWrite},
		{Base: 0x5000, Data: []byte{}, Perms: PermRead},
		{Base: 0x6000, Data: fill(4), Perms: PermRead},
	}
	l, err := NewLoader(segs)
	if err != nil {
		t.Fatalf("empty segments should not overlap: %v", err)
	}

	// An empty target cannot accept even a width-4 write.
	r := Relocation{TargetID: 0, TargetOffset: 0, Width: 4, SymbolID: 2}
	wantErr(t, l.Apply([]Relocation{r}), ErrTargetOutOfRange)
}
