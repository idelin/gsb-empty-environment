package netsec

import (
	"fmt"
	"math"
	"sort"
)

// FileRange is a half-open byte interval [Start, End) inside the signed file.
// Offsets are non-negative byte positions.
type FileRange struct {
	Start int64
	End   int64
}

// AddrRange is a half-open virtual-address interval [Start, End).
type AddrRange struct {
	Start uint64
	End   uint64
}

// Mapping describes one loader mapping of a file region into virtual memory.
//
// The mapping occupies [VAddr, VAddr+MemSize). The first FileSize bytes are
// backed by file content at [FileOff, FileOff+FileSize); the remaining
// MemSize-FileSize trailing bytes are loader zero-fill (for example .bss).
// PermX records the loader's execute permission; ExecRanges passed to
// VerifyCoverage is the authority on which addresses are executable.
type Mapping struct {
	Name     string
	FileOff  int64
	VAddr    uint64
	FileSize int64
	MemSize  int64
	PermX    bool
}

// GapReason classifies an executable byte interval that is not authenticated
// by the signature.
type GapReason string

const (
	// GapUnsignedFile marks executable file-backed bytes whose file offset is
	// not covered by any signed coverage interval.
	GapUnsignedFile GapReason = "unsigned-file"

	// GapZeroFill marks executable bytes the loader synthesized as zero-fill
	// beyond a mapping's file-backed content; such bytes never appear in the
	// file and therefore cannot be signature-covered.
	GapZeroFill GapReason = "zero-fill"

	// GapUnbacked marks executable addresses not covered by any mapping at
	// all, so no source exists to authenticate them.
	GapUnbacked GapReason = "unbacked"
)

// Gap is a half-open executable address interval [Start, End) whose bytes are
// not authenticated. Mapping is the index into the mappings slice supplied to
// VerifyCoverage, or -1 for GapUnbacked intervals. When mappings overlap, the
// same address may appear in more than one Gap, once per failing source.
type Gap struct {
	Start   uint64
	End     uint64
	Reason  GapReason
	Mapping int
	Name    string
}

// String renders a gap with its addresses, reason and originating mapping.
func (g Gap) String() string {
	if g.Mapping < 0 {
		return fmt.Sprintf("[%#x,%#x) %s", g.Start, g.End, g.Reason)
	}
	return fmt.Sprintf("[%#x,%#x) %s via mapping %d %q", g.Start, g.End, g.Reason, g.Mapping, g.Name)
}

// span is a normalized half-open uint64 interval.
type span struct {
	start uint64
	end   uint64
}

// normalizeSpans sorts spans and merges overlapping or adjacent ones.
func normalizeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	out := make([]span, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		if out[i].start != out[j].start {
			return out[i].start < out[j].start
		}
		return out[i].end < out[j].end
	})
	merged := out[:0]
	for _, s := range out {
		if len(merged) == 0 || s.start > merged[len(merged)-1].end {
			merged = append(merged, s)
			continue
		}
		if s.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = s.end
		}
	}
	return merged
}

// intersectSpan returns the intersection of two spans, if non-empty.
func intersectSpan(a, b span) (span, bool) {
	lo := a.start
	if b.start > lo {
		lo = b.start
	}
	hi := a.end
	if b.end < hi {
		hi = b.end
	}
	if lo >= hi {
		return span{}, false
	}
	return span{lo, hi}, true
}

// subtractSet returns the normalized set difference set \ b.
func subtractSet(set []span, b span) []span {
	var out []span
	for _, s := range set {
		if s.end <= b.start || s.start >= b.end {
			out = append(out, s)
			continue
		}
		if s.start < b.start {
			out = append(out, span{s.start, b.start})
		}
		if b.end < s.end {
			out = append(out, span{b.end, s.end})
		}
	}
	return normalizeSpans(out)
}

// VerifyCoverage finds every executable byte that is not authenticated by the
// signature.
//
// signed is the set of signature-covered file intervals, mappings is the
// loader's file-to-memory map (mappings may overlap or alias the same file
// region), and execRanges is the union of addresses the loader marks
// executable. All intervals are half-open.
//
// For every executable source mapping, file-backed bytes whose file offset is
// outside signed coverage are reported as GapUnsignedFile and trailing
// zero-fill bytes as GapZeroFill. Executable addresses with no mapping at all
// are reported as GapUnbacked. Overlapping mappings are checked independently:
// any executable source that fails authentication is reported.
func VerifyCoverage(signed []FileRange, mappings []Mapping, execRanges []AddrRange) ([]Gap, error) {
	signedSet, err := normalizeSigned(signed)
	if err != nil {
		return nil, err
	}
	execSet, err := normalizeExec(execRanges)
	if err != nil {
		return nil, err
	}
	if err := validateMappings(mappings); err != nil {
		return nil, err
	}
	if len(execSet) == 0 {
		return nil, nil
	}

	var gaps []Gap
	mapped := make([]span, 0, len(mappings))

	for idx, m := range mappings {
		memSpan := span{m.VAddr, m.VAddr + uint64(m.MemSize)}
		mapped = append(mapped, memSpan)

		fileSpan := span{uint64(m.FileOff), uint64(m.FileOff) + uint64(m.FileSize)}
		fileBacked := span{m.VAddr, m.VAddr + uint64(m.FileSize)}
		zeroFill := span{m.VAddr + uint64(m.FileSize), m.VAddr + uint64(m.MemSize)}

		// Signed coverage translated through this mapping's offset conversion.
		var signedMem []span
		for _, s := range signedSet {
			sub, ok := intersectSpan(s, fileSpan)
			if !ok {
				continue
			}
			signedMem = append(signedMem, span{
				start: m.VAddr + (sub.start - fileSpan.start),
				end:   m.VAddr + (sub.end - fileSpan.start),
			})
		}
		signedMem = normalizeSpans(signedMem)

		for _, exec := range execSet {
			// File-backed executable bytes outside translated signed coverage.
			if hit, ok := intersectSpan(exec, fileBacked); ok {
				bad := []span{hit}
				for _, cov := range signedMem {
					bad = subtractSet(bad, cov)
				}
				for _, g := range bad {
					gaps = append(gaps, Gap{Start: g.start, End: g.end, Reason: GapUnsignedFile, Mapping: idx, Name: m.Name})
				}
			}

			// Executable zero-fill bytes can never be in the signed file.
			if hit, ok := intersectSpan(exec, zeroFill); ok {
				gaps = append(gaps, Gap{Start: hit.start, End: hit.end, Reason: GapZeroFill, Mapping: idx, Name: m.Name})
			}
		}
	}

	// Executable addresses with no source mapping whatsoever.
	for _, exec := range execSet {
		unbacked := []span{exec}
		for _, m := range normalizeSpans(mapped) {
			unbacked = subtractSet(unbacked, m)
		}
		for _, g := range unbacked {
			gaps = append(gaps, Gap{Start: g.start, End: g.end, Reason: GapUnbacked, Mapping: -1})
		}
	}

	return mergeGaps(gaps), nil
}

func normalizeSigned(signed []FileRange) ([]span, error) {
	out := make([]span, 0, len(signed))
	for i, r := range signed {
		if r.Start < 0 || r.End < 0 || r.End < r.Start {
			return nil, fmt.Errorf("signed range %d: invalid half-open file interval [%d,%d)", i, r.Start, r.End)
		}
		if r.Start == r.End {
			continue
		}
		out = append(out, span{uint64(r.Start), uint64(r.End)})
	}
	return normalizeSpans(out), nil
}

func normalizeExec(execRanges []AddrRange) ([]span, error) {
	out := make([]span, 0, len(execRanges))
	for i, r := range execRanges {
		if r.End < r.Start {
			return nil, fmt.Errorf("exec range %d: invalid half-open address interval [%#x,%#x)", i, r.Start, r.End)
		}
		if r.Start == r.End {
			continue
		}
		out = append(out, span{r.Start, r.End})
	}
	return normalizeSpans(out), nil
}

func validateMappings(mappings []Mapping) error {
	for i, m := range mappings {
		if m.FileOff < 0 {
			return fmt.Errorf("mapping %d %q: negative FileOff %d", i, m.Name, m.FileOff)
		}
		if m.FileSize < 0 {
			return fmt.Errorf("mapping %d %q: negative FileSize %d", i, m.Name, m.FileSize)
		}
		if m.MemSize < 0 {
			return fmt.Errorf("mapping %d %q: negative MemSize %d", i, m.Name, m.MemSize)
		}
		if m.FileSize > m.MemSize {
			return fmt.Errorf("mapping %d %q: FileSize %d exceeds MemSize %d", i, m.Name, m.FileSize, m.MemSize)
		}
		if uint64(m.MemSize) > math.MaxUint64-m.VAddr {
			return fmt.Errorf("mapping %d %q: virtual span overflows address space", i, m.Name)
		}
		if m.FileSize > math.MaxInt64-m.FileOff {
			return fmt.Errorf("mapping %d %q: file span overflows int64", i, m.Name)
		}
	}
	return nil
}

// mergeGaps sorts gaps, drops duplicates and joins adjacent gaps sharing the
// same reason and source mapping.
func mergeGaps(in []Gap) []Gap {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool {
		a, b := in[i], in[j]
		switch {
		case a.Start != b.Start:
			return a.Start < b.Start
		case a.End != b.End:
			return a.End < b.End
		case a.Mapping != b.Mapping:
			return a.Mapping < b.Mapping
		default:
			return a.Reason < b.Reason
		}
	})
	out := in[:0]
	for _, g := range in {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if prev.End == g.Start && prev.Reason == g.Reason && prev.Mapping == g.Mapping && prev.Name == g.Name {
				prev.End = g.End
				continue
			}
			if prev.Start == g.Start && prev.End == g.End && prev.Reason == g.Reason && prev.Mapping == g.Mapping {
				continue
			}
		}
		out = append(out, g)
	}
	return out
}
