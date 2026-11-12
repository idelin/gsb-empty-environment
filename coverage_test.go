package netsec

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// byteGap records the reason and source mapping index for a single
// executable byte as determined by the independent byte-enumeration oracle.
type byteGap struct {
	reason  GapReason
	mapping int
}

// oracleVerify is a deliberately simple, independent implementation: it
// enumerates every executable byte one at a time and classifies it directly
// from the raw inputs, without sharing any interval arithmetic with the
// production code. fileLen bounds the offsets the loader may read.
func oracleVerify(t *testing.T, signed []FileRange, mappings []Mapping, execRanges []AddrRange, fileLen int64) map[uint64][]byteGap {
	t.Helper()

	signedAt := func(off int64) bool {
		for _, r := range signed {
			if off >= r.Start && off < r.End {
				return true
			}
		}
		return false
	}
	execAt := func(addr uint64) bool {
		for _, r := range execRanges {
			if addr >= r.Start && addr < r.End {
				return true
			}
		}
		return false
	}

	gaps := map[uint64][]byteGap{}

	var lo, hi uint64
	for i, r := range execRanges {
		if i == 0 || r.Start < lo {
			lo = r.Start
		}
		if r.End > hi {
			hi = r.End
		}
	}

	for addr := lo; addr < hi; addr++ {
		if !execAt(addr) {
			continue
		}
		anySource := false
		for idx, m := range mappings {
			memEnd := m.VAddr + uint64(m.MemSize)
			if addr < m.VAddr || addr >= memEnd {
				continue
			}
			anySource = true
			rel := int64(addr - m.VAddr)
			if rel < m.FileSize {
				off := m.FileOff + rel
				if off >= fileLen {
					t.Fatalf("mapping %d reads past file: offset %d >= len %d", idx, off, fileLen)
				}
				if !signedAt(off) {
					gaps[addr] = append(gaps[addr], byteGap{GapUnsignedFile, idx})
				}
			} else {
				gaps[addr] = append(gaps[addr], byteGap{GapZeroFill, idx})
			}
		}
		if !anySource {
			gaps[addr] = append(gaps[addr], byteGap{GapUnbacked, -1})
		}
	}

	for addr, gs := range gaps {
		sorted := append([]byteGap(nil), gs...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].mapping != sorted[j].mapping {
				return sorted[i].mapping < sorted[j].mapping
			}
			return sorted[i].reason < sorted[j].reason
		})
		gaps[addr] = sorted
	}
	return gaps
}

// gapCoverage expands production gaps back to per-byte classifications so the
// two implementations can be compared on identical inputs.
func gapCoverage(gaps []Gap) map[uint64][]byteGap {
	out := map[uint64][]byteGap{}
	for _, g := range gaps {
		for addr := g.Start; addr < g.End; addr++ {
			out[addr] = append(out[addr], byteGap{g.Reason, g.Mapping})
		}
	}
	for addr := range out {
		sort.Slice(out[addr], func(i, j int) bool {
			if out[addr][i].mapping != out[addr][j].mapping {
				return out[addr][i].mapping < out[addr][j].mapping
			}
			return out[addr][i].reason < out[addr][j].reason
		})
	}
	return out
}

func assertMatchesOracle(t *testing.T, signed []FileRange, mappings []Mapping, execRanges []AddrRange, fileLen int64) []Gap {
	t.Helper()
	gaps, err := VerifyCoverage(signed, mappings, execRanges)
	if err != nil {
		t.Fatalf("VerifyCoverage: %v", err)
	}
	want := oracleVerify(t, signed, mappings, execRanges, fileLen)
	got := gapCoverage(gaps)

	for _, g := range gaps {
		if g.End <= g.Start {
			t.Fatalf("empty or inverted gap: %+v", g)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("byte set mismatch: got %d bad bytes, want %d\ngot gaps: %+v", len(got), len(want), gaps)
	}
	for addr, wantGs := range want {
		gotGs, ok := got[addr]
		if !ok {
			t.Fatalf("address %#x missing from gaps: want %+v", addr, wantGs)
		}
		if len(gotGs) != len(wantGs) {
			t.Fatalf("address %#x: got %+v want %+v", addr, gotGs, wantGs)
		}
		for i := range wantGs {
			if gotGs[i] != wantGs[i] {
				t.Fatalf("address %#x: got %+v want %+v", addr, gotGs, wantGs)
			}
		}
	}
	return gaps
}

// writeSmallFile creates a small on-disk file whose bytes carry distinct
// values so offset/alias enumeration can verify the loader reads each byte.
func writeSmallFile(t *testing.T, path string, size int) string {
	t.Helper()
	path = filepath.Join(t.TempDir(), path)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(0xA0 + (i % 0x40))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got := len(mustReadFile(t, path)); got != size {
		t.Fatalf("file len = %d, want %d", got, size)
	}
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return data
}

func gapAddressRanges(gaps []Gap, reason GapReason) []AddrRange {
	var out []AddrRange
	for _, g := range gaps {
		if g.Reason == reason {
			out = append(out, AddrRange{g.Start, g.End})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

func assertAddrRanges(t *testing.T, got, want []AddrRange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("range count = %d, want %d\ngot  %+v\nwant %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestFullySignedByteEnumeration uses a real small file and enumerates every
// byte: a fully covered mapping produces no gaps at all.
func TestFullySignedByteEnumeration(t *testing.T) {
	const n = 16
	path := writeSmallFile(t, "fully.bin", n)
	file := mustReadFile(t, path)

	signed := []FileRange{{0, int64(len(file))}}
	mappings := []Mapping{{
		Name: "code", FileOff: 0, VAddr: 0x1000, FileSize: int64(len(file)), MemSize: int64(len(file)), PermX: true,
	}}
	execRanges := []AddrRange{{0x1000, 0x1000 + uint64(len(file))}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))
	if len(gaps) != 0 {
		t.Fatalf("expected no gaps, got %+v", gaps)
	}
}

// TestExcludedIntervals checks signature coverage that excludes middle and
// edge file regions: each excluded file byte must surface at its mapped VA.
func TestExcludedIntervals(t *testing.T) {
	const n = 24
	path := writeSmallFile(t, "excluded.bin", n)
	file := mustReadFile(t, path)

	signed := []FileRange{{2, 6}, {10, 20}}
	mappings := []Mapping{{
		Name: "code", FileOff: 0, VAddr: 0x2000, FileSize: n, MemSize: n, PermX: true,
	}}
	execRanges := []AddrRange{{0x2000, 0x2000 + n}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))
	assertAddrRanges(t, gapAddressRanges(gaps, GapUnsignedFile),
		[]AddrRange{{0x2000, 0x2002}, {0x2006, 0x200A}, {0x2014, 0x2018}})
}

// TestZeroFillBeyondFile maps more memory than file content (.bss style) and
// marks the zero-fill region executable: it must be reported even though the
// file-backed prefix is fully signed.
func TestZeroFillBeyondFile(t *testing.T) {
	const n = 8
	path := writeSmallFile(t, "zerofill.bin", n)
	file := mustReadFile(t, path)

	signed := []FileRange{{0, n}}
	mappings := []Mapping{{
		Name: "data", FileOff: 0, VAddr: 0x3000, FileSize: n, MemSize: n + 12, PermX: true,
	}}
	execRanges := []AddrRange{{0x3000, 0x3000 + n + 12}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))
	assertAddrRanges(t, gapAddressRanges(gaps, GapZeroFill),
		[]AddrRange{{0x3000 + n, 0x3000 + n + 12}})
	if len(gapAddressRanges(gaps, GapUnsignedFile)) != 0 {
		t.Fatalf("file-backed region is signed, but got unsigned gaps: %+v", gaps)
	}
}

// TestPaddingOutsideSegment covers segment-outside padding: the executable
// range extends past the mapping entirely, while a second mapping has a
// file-backed tail outside signed coverage.
func TestPaddingOutsideSegment(t *testing.T) {
	const n = 10
	path := writeSmallFile(t, "padding.bin", n)
	file := mustReadFile(t, path)

	signed := []FileRange{{0, 4}}
	mappings := []Mapping{
		{Name: "seg0", FileOff: 0, VAddr: 0x4000, FileSize: n, MemSize: n, PermX: true},
		{Name: "seg1", FileOff: 0, VAddr: 0x5000, FileSize: 6, MemSize: 6, PermX: true},
	}
	// Executable bytes run 0x14 beyond seg0's end; seg1 is fully executable.
	execRanges := []AddrRange{
		{0x4000, 0x4000 + n + 0x14},
		{0x5000, 0x5006},
	}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))

	assertAddrRanges(t, gapAddressRanges(gaps, GapUnsignedFile), []AddrRange{
		{0x4004, 0x400A},
		{0x5004, 0x5006},
	})
	assertAddrRanges(t, gapAddressRanges(gaps, GapUnbacked), []AddrRange{
		{0x400A, 0x401E},
	})
}

// TestAliasOverlap maps a signed file region twice (aliases) plus an unsigned
// alias. Every executable source is checked independently.
func TestAliasOverlap(t *testing.T) {
	const n = 8
	path := writeSmallFile(t, "alias.bin", n)
	file := mustReadFile(t, path)

	signed := []FileRange{{0, n}}
	mappings := []Mapping{
		{Name: "primary", FileOff: 0, VAddr: 0x6000, FileSize: n, MemSize: n, PermX: true},
		{Name: "alias-signed", FileOff: 0, VAddr: 0x6000, FileSize: n, MemSize: n, PermX: true},
		{Name: "alias-unsigned", FileOff: 16, VAddr: 0x7000, FileSize: n, MemSize: n, PermX: true},
	}
	execRanges := []AddrRange{{0x6000, 0x6000 + n}, {0x7000, 0x7000 + n}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file))+16)

	// Two signed aliases at 0x6000 produce no gap; the third source points at
	// unsigned file offsets [16,24) and must be reported per byte.
	assertAddrRanges(t, gapAddressRanges(gaps, GapUnsignedFile),
		[]AddrRange{{0x7000, 0x7008}})
	if len(gapAddressRanges(gaps, GapZeroFill)) != 0 || len(gapAddressRanges(gaps, GapUnbacked)) != 0 {
		t.Fatalf("unexpected zero-fill/unbacked gaps: %+v", gaps)
	}
}

// TestOverlappingMappingsOneUnsigned places two overlapping mappings over the
// same executable bytes; only the one whose file offsets are unsigned reports.
func TestOverlappingMappingsOneUnsigned(t *testing.T) {
	const n = 8
	path := writeSmallFile(t, "overlap.bin", n)
	_ = mustReadFile(t, path)

	signed := []FileRange{{0, n}}
	mappings := []Mapping{
		{Name: "good", FileOff: 0, VAddr: 0x8000, FileSize: n, MemSize: n, PermX: true},
		{Name: "evil", FileOff: 32, VAddr: 0x8000, FileSize: n, MemSize: n, PermX: true},
	}
	execRanges := []AddrRange{{0x8000, 0x8000 + n}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, 40)

	if len(gaps) != 1 {
		t.Fatalf("expected exactly one gap source, got %+v", gaps)
	}
	if gaps[0].Reason != GapUnsignedFile || gaps[0].Name != "evil" {
		t.Fatalf("expected unsigned-file gap from evil mapping, got %+v", gaps)
	}
}

// TestOffsetConversionSignedSubrange uses non-zero FileOff/VAddr: signed
// coverage partially intersects the mapped file window and the gap lands on
// the correct virtual addresses after offset conversion.
func TestOffsetConversionSignedSubrange(t *testing.T) {
	const n = 12
	path := writeSmallFile(t, "offset.bin", n+8)
	file := mustReadFile(t, path)

	mappings := []Mapping{{
		Name: "code", FileOff: 4, VAddr: 0x9000, FileSize: n, MemSize: n, PermX: true,
	}}
	// Signed file offsets [6,12) map to VA [0x9002,0x9008).
	signed := []FileRange{{6, 12}}
	execRanges := []AddrRange{{0x9000, 0x9000 + n}}

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))
	assertAddrRanges(t, gapAddressRanges(gaps, GapUnsignedFile),
		[]AddrRange{{0x9000, 0x9002}, {0x9008, 0x900C}})
}

// TestMergedRangesAndEmptyInputs verifies adjacent-gap coalescing, overlapping
// input ranges and empty/edge inputs against the byte oracle.
func TestMergedRangesAndEmptyInputs(t *testing.T) {
	path := writeSmallFile(t, "merge.bin", 10)
	file := mustReadFile(t, path)

	mappings := []Mapping{{
		Name: "code", FileOff: 0, VAddr: 0xA000, FileSize: 10, MemSize: 10, PermX: true,
	}}
	// Overlapping exec ranges must be normalized into one contiguous span.
	execRanges := []AddrRange{{0xA000, 0xA006}, {0xA004, 0xA00A}}
	signed := []FileRange{{2, 3}, {3, 7}} // adjacent/overlapping signed pieces

	gaps := assertMatchesOracle(t, signed, mappings, execRanges, int64(len(file)))
	assertAddrRanges(t, gapAddressRanges(gaps, GapUnsignedFile),
		[]AddrRange{{0xA000, 0xA002}, {0xA007, 0xA00A}})

	// No executable addresses means no gaps and no error.
	empty, err := VerifyCoverage(signed, mappings, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty exec: gaps=%+v err=%v", empty, err)
	}
}

// TestInvalidInputs exercises input validation and overflow guards.
func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		signed   []FileRange
		mappings []Mapping
		exec     []AddrRange
	}{
		{"bad signed range", []FileRange{{5, 4}}, nil, []AddrRange{{1, 2}}},
		{"negative file offset", nil, []Mapping{{FileOff: -1, VAddr: 0, FileSize: 1, MemSize: 1}}, []AddrRange{{0, 1}}},
		{"file size exceeds mem size", nil, []Mapping{{FileOff: 0, VAddr: 0, FileSize: 2, MemSize: 1}}, []AddrRange{{0, 1}}},
		{"va overflow", nil, []Mapping{{FileOff: 0, VAddr: ^uint64(0) - 1, FileSize: 0, MemSize: 4}}, []AddrRange{{^uint64(0) - 1, ^uint64(0)}}},
		{"bad exec range", nil, nil, []AddrRange{{9, 8}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifyCoverage(tc.signed, tc.mappings, tc.exec); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestGapString renders both mapped and unbacked gaps.
func TestGapString(t *testing.T) {
	if got := (Gap{Start: 0x10, End: 0x20, Reason: GapUnbacked, Mapping: -1}).String(); got == "" {
		t.Fatal("empty unbacked string")
	}
	if got := (Gap{Start: 0x10, End: 0x20, Reason: GapZeroFill, Mapping: 2, Name: "bss"}).String(); got == "" {
		t.Fatal("empty mapped string")
	}
}

// TestRandomizedAgainstOracle fuzzes small layouts (random coverage holes,
// zero-fill tails, overlaps and aliases) and requires the interval-based
// production implementation to match per-byte oracle classification exactly.
func TestRandomizedAgainstOracle(t *testing.T) {
	const iterations = 500
	rng := rand.New(rand.NewPCG(0x365, 0x911))

	for iter := 0; iter < iterations; iter++ {
		fileLen := int64(1 + rng.IntN(40))

		mapCount := 1 + rng.IntN(4)
		mappings := make([]Mapping, mapCount)
		for i := range mappings {
			fileSize := int64(rng.IntN(int(fileLen) + 1))
			var fileOff int64
			if fileSize > 0 {
				fileOff = rng.Int64N(fileLen - fileSize + 1)
			} else {
				fileOff = rng.Int64N(fileLen + 1)
			}
			extra := int64(rng.IntN(8))
			vaddr := uint64(0x1000*(1+i)) + uint64(rng.IntN(0x40))
			mappings[i] = Mapping{
				Name:     "m",
				FileOff:  fileOff,
				VAddr:    vaddr,
				FileSize: fileSize,
				MemSize:  fileSize + extra,
				PermX:    true,
			}
		}

		signedCount := rng.IntN(4)
		signed := make([]FileRange, 0, signedCount)
		for j := 0; j < signedCount; j++ {
			start := rng.Int64N(fileLen + 1)
			end := start + int64(rng.IntN(int(fileLen-start)+1))
			signed = append(signed, FileRange{start, end})
		}

		execRanges := make([]AddrRange, 0, mapCount)
		for _, m := range mappings {
			memEnd := m.VAddr + uint64(m.MemSize)
			if memEnd == m.VAddr {
				continue
			}
			start := m.VAddr - uint64(rng.IntN(3))
			end := memEnd + uint64(rng.IntN(4))
			execRanges = append(execRanges, AddrRange{start, end})
		}

		assertMatchesOracle(t, signed, mappings, execRanges, fileLen)
	}
}
