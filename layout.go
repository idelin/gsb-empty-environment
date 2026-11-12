package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Perm is a bitmask of access permissions on mapped memory.
type Perm uint8

const (
	PermR Perm = 1 << iota // read
	PermW                  // write
	PermX                  // execute
)

// permWX is the dangerous writable-and-executable combination.
const permWX = PermW | PermX

// Has reports whether p contains every permission bit of q.
func (p Perm) Has(q Perm) bool { return p&q == q }

// Dangerous reports whether p is both writable and executable.
func (p Perm) Dangerous() bool { return p&permWX == permWX }

func (p Perm) String() string {
	out := []byte("---")
	if p&PermR != 0 {
		out[0] = 'r'
	}
	if p&PermW != 0 {
		out[1] = 'w'
	}
	if p&PermX != 0 {
		out[2] = 'x'
	}
	return string(out)
}

// Segment describes one mapped segment of a file image.
//
// The file-backed part [VAddr, VAddr+FileSize) is loaded from file range
// [FileOff, FileOff+FileSize); the remainder [VAddr+FileSize, VAddr+MemSize)
// is zero-filled (BSS) and is mapped with the same permissions, so it
// contributes to page permission composition exactly like file-backed bytes.
type Segment struct {
	FileOff  uint64
	VAddr    uint64
	FileSize uint64
	MemSize  uint64
	Perm     Perm
}

var (
	// ErrPageSize is returned when the page size is zero.
	ErrPageSize = errors.New("netsec: page size must be non-zero")
	// ErrSizeOrder is returned when a segment's MemSize is smaller than
	// its FileSize.
	ErrSizeOrder = errors.New("netsec: MemSize is smaller than FileSize")
	// ErrOverflow is returned when a segment's virtual address range
	// [VAddr, VAddr+MemSize) does not fit in a uint64.
	ErrOverflow = errors.New("netsec: segment address range overflows uint64")
	// ErrMisaligned is returned when a file-backed segment's VAddr and
	// FileOff are not congruent modulo the page size, which no paged
	// loader can map directly.
	ErrMisaligned = errors.New("netsec: VAddr and FileOff differ modulo page size")
)

// DangerRange is a run of consecutive pages whose composed permission is
// dangerous. It covers pages [Start, Start+Pages*PageSize); the end may
// reach 2^64, in which case it is not representable as a uint64.
type DangerRange struct {
	Start uint64 // page-aligned virtual address of the first page
	Pages uint64 // number of consecutive dangerous pages
	Perm  Perm   // composed permission shared by every page in the run
}

// SegmentSet is a minimal set of input segment indices whose permission
// union is dangerous: removing any member makes the union non-dangerous.
type SegmentSet struct {
	Segments []int // indices into the Analyze input, ascending
	Perm     Perm  // union of the members' permissions
}

// Report is the result of Analyze.
type Report struct {
	PageSize  uint64
	Dangerous []DangerRange // ascending by Start, non-overlapping
	Sets      []SegmentSet  // deduplicated minimal sets, sorted
}

// HasDanger reports whether any dangerous page was found.
func (r Report) HasDanger() bool { return len(r.Dangerous) > 0 }

// DangerousPages returns the total number of dangerous pages.
func (r Report) DangerousPages() uint64 {
	var n uint64
	for _, d := range r.Dangerous {
		n += d.Pages
	}
	return n
}

// interval is a validated, non-empty mapped range.
type interval struct {
	start, end uint64
	perm       Perm
	idx        int
}

// Analyze composes page-level permissions for a loader layout.
//
// Every page intersected by a segment's mapped range [VAddr, VAddr+MemSize)
// - file-backed bytes and zero-fill alike - inherits that segment's
// permissions; a page covered by several segments receives the union of all
// of them. Pages whose union is both writable and executable are reported
// in Report.Dangerous, and Report.Sets lists the minimal segment sets whose
// combined permissions produce such pages.
func Analyze(segs []Segment, pageSize uint64) (Report, error) {
	if pageSize == 0 {
		return Report{}, ErrPageSize
	}
	ivs := make([]interval, 0, len(segs))
	for i, s := range segs {
		if s.MemSize < s.FileSize {
			return Report{}, fmt.Errorf("%w: segment %d has MemSize %d < FileSize %d",
				ErrSizeOrder, i, s.MemSize, s.FileSize)
		}
		end := s.VAddr + s.MemSize
		if end < s.VAddr {
			return Report{}, fmt.Errorf("%w: segment %d [VAddr %#x + MemSize %#x)",
				ErrOverflow, i, s.VAddr, s.MemSize)
		}
		if s.FileSize > 0 && s.VAddr%pageSize != s.FileOff%pageSize {
			return Report{}, fmt.Errorf("%w: segment %d VAddr %#x FileOff %#x",
				ErrMisaligned, i, s.VAddr, s.FileOff)
		}
		if s.MemSize == 0 {
			continue
		}
		ivs = append(ivs, interval{s.VAddr, end, s.Perm, i})
	}
	rep := Report{PageSize: pageSize}
	if len(ivs) == 0 {
		return rep, nil
	}

	bounds := make([]uint64, 0, 2*len(ivs))
	for _, iv := range ivs {
		bounds = append(bounds, iv.start, iv.end)
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })
	uniq := bounds[:0]
	for _, b := range bounds {
		if len(uniq) == 0 || uniq[len(uniq)-1] != b {
			uniq = append(uniq, b)
		}
	}
	bounds = uniq

	setSeen := make(map[string]struct{})
	activeSeen := make(map[string]struct{})

	// emit records the finalized pages [first, last], all sharing perm and
	// covered by exactly the segments active (indices into ivs).
	emit := func(first, last uint64, perm Perm, active []int) {
		if !perm.Dangerous() {
			return
		}
		merged := false
		if n := len(rep.Dangerous); n > 0 {
			tail := &rep.Dangerous[n-1]
			if tail.Perm == perm && first-tail.Start/pageSize == tail.Pages {
				tail.Pages += last - first + 1
				merged = true
			}
		}
		if !merged {
			rep.Dangerous = append(rep.Dangerous, DangerRange{
				Start: first * pageSize,
				Pages: last - first + 1,
				Perm:  perm,
			})
		}
		aKey := keyOf(active)
		if _, ok := activeSeen[aKey]; ok {
			return
		}
		activeSeen[aKey] = struct{}{}
		for _, set := range minimalSets(active, ivs) {
			sKey := keyOf(set)
			if _, ok := setSeen[sKey]; ok {
				continue
			}
			setSeen[sKey] = struct{}{}
			var p Perm
			for _, idx := range set {
				p |= segs[idx].Perm
			}
			rep.Sets = append(rep.Sets, SegmentSet{Segments: set, Perm: p})
		}
	}

	var (
		curPage   uint64
		curPerm   Perm
		curActive []int
		haveCur   bool
	)
	flush := func() {
		if haveCur {
			emit(curPage, curPage, curPerm, curActive)
		}
		haveCur = false
		curPerm = 0
		curActive = nil
	}

	// Sweep elementary intervals [b0, b1) on which the active segment set
	// is constant, accumulating page permissions in ascending page order.
	for bi := 0; bi+1 < len(bounds); bi++ {
		b0, b1 := bounds[bi], bounds[bi+1]
		var active []int
		var perm Perm
		for k := range ivs {
			if ivs[k].start <= b0 && b1 <= ivs[k].end {
				active = append(active, k)
				perm |= ivs[k].perm
			}
		}
		if len(active) == 0 {
			continue // unmapped gap; page state carries over
		}
		pFirst := b0 / pageSize
		pLast := (b1 - 1) / pageSize
		if haveCur && pFirst > curPage {
			flush()
		}
		if !haveCur {
			haveCur = true
			curPage = pFirst
		}
		curPerm |= perm
		curActive = mergeInts(curActive, active)
		if pLast > pFirst {
			// Page pFirst is final: later intervals start at or past
			// b1, beyond its last byte.
			flush()
			if pLast > pFirst+1 {
				// Pages strictly inside [b0, b1) are final too,
				// with exactly this interval's permission and
				// active set.
				emit(pFirst+1, pLast-1, perm, active)
			}
			haveCur = true
			curPage = pLast
			curPerm = perm
			curActive = append([]int(nil), active...)
		}
	}
	flush()

	sort.Slice(rep.Sets, func(i, j int) bool {
		a, b := rep.Sets[i].Segments, rep.Sets[j].Segments
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	return rep, nil
}

// minimalSets returns the minimal dangerous subsets of active (indices into
// ivs) as original segment indices. Because the dangerous condition involves
// only two permission bits, every minimal set is a single segment that is
// itself writable-executable, or a pair contributing one bit each.
func minimalSets(active []int, ivs []interval) [][]int {
	var out [][]int
	for _, k := range active {
		if ivs[k].perm.Dangerous() {
			out = append(out, []int{ivs[k].idx})
		}
	}
	for ai := 0; ai < len(active); ai++ {
		i := active[ai]
		if ivs[i].perm.Dangerous() {
			continue
		}
		for bi := ai + 1; bi < len(active); bi++ {
			j := active[bi]
			if ivs[j].perm.Dangerous() {
				continue
			}
			if (ivs[i].perm | ivs[j].perm).Dangerous() {
				out = append(out, []int{ivs[i].idx, ivs[j].idx})
			}
		}
	}
	return out
}

// mergeInts unions two sorted, deduplicated index slices.
func mergeInts(a, b []int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func keyOf(idxs []int) string {
	var sb strings.Builder
	for _, i := range idxs {
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte(',')
	}
	return sb.String()
}
