// Package allocplan plans tagged allocations for a Go memory host that needs
// to detect dangling pointers with a finite tag domain.
//
// A tag is "poisoned" for an address unit during a quarantine interval after
// the last allocation carrying that tag was released there. Reusing the tag
// on the same unit before the interval ends would make an old dangling
// pointer valid again, so the planner only hands out tags that are neither
// in use nor quarantined on any unit of the requested range. When every tag
// is unavailable the planner never silently reuses one: Alloc returns an
// explicit rejection and, when waiting can help, reports the virtual tick at
// which a tag becomes eligible again.
//
// Quarantine history is stored per address unit. Splitting a segment divides
// the per-unit history between the pieces and merging adjacent free segments
// concatenates it, so the quarantine survives every range reorganization and
// stays precise enough to allow partial interval reuse.
package allocplan

import "errors"

// Tag identifies a memory version. Zero is reserved as the free-segment
// sentinel; allocatable tags are numbered 1..TagDomainSize.
type Tag uint16

// NoTag marks a free segment.
const NoTag Tag = 0

// Rejection conditions returned, possibly wrapped, by Alloc.
var (
	// ErrRejected is the base error for every allocation that cannot be
	// satisfied without breaking tag safety.
	ErrRejected = errors.New("allocplan: allocation rejected")
	// ErrQuarantined means every free tag is still inside its quarantine
	// interval; waiting until the reported tick can make one eligible.
	ErrQuarantined = errors.New("allocplan: all candidate tags quarantined")
	// ErrAllTagsBusy means every tag currently labels a live allocation, so
	// no amount of waiting on the clock alone can help.
	ErrAllTagsBusy = errors.New("allocplan: all tags are in use")
	// ErrNoAddressSpace means no free run is large enough.
	ErrNoAddressSpace = errors.New("allocplan: insufficient free address space")

	// ErrBadConfig is returned by New when the planner configuration is invalid.
	ErrBadConfig = errors.New("allocplan: invalid configuration")
	// ErrBadSegment is returned by Free and Split when the requested segment
	// does not identify an allocated segment.
	ErrBadSegment = errors.New("allocplan: segment is not allocated")
	// ErrBadSize is returned when a request has a non-positive size or a
	// split point leaves an empty piece.
	ErrBadSize = errors.New("allocplan: invalid size")
)

// ExhaustedError attaches the earliest virtual tick at which waiting could
// make progress to a rejected allocation. WaitTick is only meaningful when
// Waitable is true.
type ExhaustedError struct {
	Err      error
	WaitTick int64
	Waitable bool
}

func (e *ExhaustedError) Error() string {
	if e.Waitable {
		return e.Err.Error() + "; retry not before tick " + itoa(e.WaitTick)
	}
	return e.Err.Error()
}

// Unwrap lets errors.Is reach the wrapped rejection cause and ErrRejected.
func (e *ExhaustedError) Unwrap() []error { return []error{e.Err, ErrRejected} }

// Is reports ExhaustedError as matching ErrRejected directly so callers can
// use a single errors.Is(err, ErrRejected) check.
func (e *ExhaustedError) Is(target error) bool { return target == ErrRejected }

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		d := v % 10
		if d < 0 {
			d = -d
		}
		i--
		buf[i] = byte('0' + d)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Clock is a monotonically non-decreasing virtual time source driven by the
// host rather than wall time. Deterministic ticks make quarantine decisions
// reproducible and exhaustively testable.
type Clock struct {
	tick int64
}

// Tick returns the current virtual tick.
func (c *Clock) Tick() int64 { return c.tick }

// Advance moves the clock forward by d ticks. Zero and negative deltas leave
// the clock unchanged so callers cannot rewind quarantine history.
func (c *Clock) Advance(d int64) {
	if d > 0 {
		c.tick = saturatingAdd(c.tick, d)
	}
}

// AdvanceTo moves the clock to t when t is in the future.
func (c *Clock) AdvanceTo(t int64) {
	if t > c.tick {
		c.tick = t
	}
}

// Config sizes a planner. Only the span [0, AddressSpace) is managed.
type Config struct {
	TagDomainSize int   // number of distinct allocatable tags; must be positive
	AddressSpace  int   // number of address units; must be positive
	Quarantine    int64 // quarantine interval measured in virtual ticks
}

// Segment describes one contiguous managed range exposed to the host. The
// planner keeps the authoritative per-unit quarantine history internally.
type Segment struct {
	Start int
	End   int // exclusive
	// Tag is NoTag for free segments and the live label for allocated ones.
	Tag Tag
}

// Length reports the segment size in address units.
func (s Segment) Length() int { return s.End - s.Start }

// Planner is a single-threaded allocation planner driven by an explicit
// virtual clock. It is safe for use by exactly one host goroutine at a time.
type Planner struct {
	clock    Clock
	tagCount int
	space    int
	quar     int64
	segments []*Segment
	// history[address][tag] is the absolute tick until which tag is poisoned
	// at that address; -1 means the tag was never released there. Expired
	// entries are retained as history and never reset.
	history [][]int64
	live    map[Tag]int
}

// New validates the configuration and returns a planner whose whole address
// space is one free segment with an empty quarantine history.
func New(cfg Config) (*Planner, error) {
	if cfg.TagDomainSize <= 0 || cfg.AddressSpace <= 0 || cfg.Quarantine < 0 {
		return nil, ErrBadConfig
	}
	if cfg.TagDomainSize > int(^Tag(0)) {
		return nil, ErrBadConfig
	}
	history := make([][]int64, cfg.AddressSpace)
	for addr := range history {
		row := make([]int64, cfg.TagDomainSize+1)
		for tag := range row {
			row[tag] = -1
		}
		history[addr] = row
	}
	return &Planner{
		tagCount: cfg.TagDomainSize,
		space:    cfg.AddressSpace,
		quar:     cfg.Quarantine,
		segments: []*Segment{{Start: 0, End: cfg.AddressSpace, Tag: NoTag}},
		history:  history,
		live:     map[Tag]int{},
	}, nil
}

// Clock returns the virtual clock backing the planner.
func (p *Planner) Clock() *Clock { return &p.clock }

// Segments returns a point-in-time copy of the segment table.
func (p *Planner) Segments() []Segment {
	out := make([]Segment, len(p.segments))
	for i, seg := range p.segments {
		out[i] = Segment{Start: seg.Start, End: seg.End, Tag: seg.Tag}
	}
	return out
}

// QuarantineUntil reports the retained quarantine expiry tick of tag at the
// given address. The boolean is false when the address or tag is out of range
// or the tag has never been poisoned there.
func (p *Planner) QuarantineUntil(address int, tag Tag) (int64, bool) {
	if address < 0 || address >= p.space || tag == NoTag || int(tag) > p.tagCount {
		return 0, false
	}
	until := p.history[address][tag]
	if until < 0 {
		return 0, false
	}
	return until, true
}

// Alloc reserves size address units from the first fitting free segment and
// labels the new allocation with a tag that currently has no live references
// and is not quarantined on any unit of the range. On success it returns the
// segment start and the chosen tag.
//
// Failure is always explicit: the returned error wraps ErrRejected and the
// planner state is left untouched. When every tag is merely quarantined the
// error is an *ExhaustedError carrying the earliest retry tick.
func (p *Planner) Alloc(size int) (start int, tag Tag, err error) {
	if size <= 0 {
		return 0, NoTag, ErrBadSize
	}
	now := p.clock.Tick()
	for _, seg := range p.segments {
		if seg.Tag != NoTag || seg.Length() < size {
			continue
		}
		chosen, until, ok := p.pickTag(seg.Start, size, now)
		if !ok {
			return 0, NoTag, p.exhausted(until)
		}
		allocated := &Segment{Start: seg.Start, End: seg.Start + size, Tag: chosen}
		replacements := []*Segment{allocated}
		if seg.Length() > size {
			replacements = append(replacements, &Segment{
				Start: seg.Start + size,
				End:   seg.End,
				Tag:   NoTag,
			})
		}
		p.replaceSegment(seg, replacements)
		p.live[chosen]++
		return allocated.Start, chosen, nil
	}
	return 0, NoTag, ErrNoAddressSpace
}

// pickTag scans the finite tag domain in deterministic order for the segment
// range [start, start+size). On success the chosen tag is returned;
// otherwise until is the earliest tick at which a quarantined tag becomes
// eligible somewhere in the range, or zero when every tag has live refs.
func (p *Planner) pickTag(start, size int, now int64) (Tag, int64, bool) {
	var earliest int64
	anyQuarantined := false
	for candidate := Tag(1); int(candidate) <= p.tagCount; candidate++ {
		if p.live[candidate] > 0 {
			continue
		}
		blocking := false
		var expiry int64
		for addr := start; addr < start+size; addr++ {
			if p.history[addr][candidate] > now {
				blocking = true
				if expiry == 0 || p.history[addr][candidate] < expiry {
					expiry = p.history[addr][candidate]
				}
			}
		}
		if !blocking {
			return candidate, 0, true
		}
		if !anyQuarantined || expiry < earliest {
			earliest = expiry
			anyQuarantined = true
		}
	}
	return NoTag, earliest, false
}

func (p *Planner) exhausted(until int64) error {
	if until == 0 {
		return &ExhaustedError{Err: ErrAllTagsBusy}
	}
	return &ExhaustedError{Err: ErrQuarantined, WaitTick: until, Waitable: true}
}

// NextAvailableTick reports the earliest virtual tick at which Alloc of size
// units could succeed, assuming no intervening allocation. The boolean is
// false when no range can fit or every tag has a live reference.
func (p *Planner) NextAvailableTick(size int) (int64, bool) {
	if size <= 0 {
		return 0, false
	}
	now := p.clock.Tick()
	best := int64(0)
	found := false
	for _, seg := range p.segments {
		if seg.Tag != NoTag || seg.Length() < size {
			continue
		}
		_, until, ok := p.pickTag(seg.Start, size, now)
		if ok {
			return now, true
		}
		if until != 0 && (!found || until < best) {
			best = until
			found = true
		}
	}
	return best, found
}

// Eligible reports whether tag may be handed out on [start, end) at the
// current tick. A tag with live references, or one still quarantined on any
// overlapped unit, is ineligible.
func (p *Planner) Eligible(tag Tag, start, end int) bool {
	if tag == NoTag || int(tag) > p.tagCount || p.live[tag] > 0 {
		return false
	}
	if start < 0 || end <= start || end > p.space {
		return false
	}
	now := p.clock.Tick()
	for addr := start; addr < end; addr++ {
		if p.history[addr][tag] > now {
			return false
		}
	}
	return true
}

// Free releases the allocated segment starting at start. Its tag is poisoned
// on every unit of the range until the current tick plus the quarantine
// interval, the segment becomes free, and adjacent free segments are merged.
// Per-unit quarantine history is untouched by the merge, so a tag released on
// one part never becomes blocked on a neighboring part that never hosted it.
func (p *Planner) Free(start int) error {
	seg := p.findStart(start)
	if seg == nil || seg.Tag == NoTag {
		return ErrBadSegment
	}
	released := seg.Tag
	now := p.clock.Tick()
	expiry := saturatingAdd(now, p.quar)
	for addr := seg.Start; addr < seg.End; addr++ {
		p.history[addr][released] = expiry
	}

	p.live[released]--
	if p.live[released] == 0 {
		delete(p.live, released)
	}

	seg.Tag = NoTag
	p.coalesce(seg)
	return nil
}

// Split divides the allocated segment starting at start into
// [start, start+left) and [start+left, oldEnd). Both pieces stay allocated
// to the same tag. Quarantine history is per address unit, so each piece
// automatically keeps exactly the history of its own units. The second
// segment's start is returned.
func (p *Planner) Split(start, left int) (secondStart int, err error) {
	if left <= 0 {
		return 0, ErrBadSize
	}
	seg := p.findStart(start)
	if seg == nil || seg.Tag == NoTag {
		return 0, ErrBadSegment
	}
	if left >= seg.Length() {
		return 0, ErrBadSize
	}
	mid := seg.Start + left
	first := &Segment{Start: seg.Start, End: mid, Tag: seg.Tag}
	second := &Segment{Start: mid, End: seg.End, Tag: seg.Tag}
	p.replaceSegment(seg, []*Segment{first, second})
	p.live[seg.Tag]++
	// Each piece becomes an independently releasable reference to the same tag,
	// so the live reference count grows by one and each Free drops it once.
	return mid, nil
}

// coalesce merges anchor with every adjacent free segment. The per-unit
// history matrix is unchanged, so no quarantine is lost or widened.
func (p *Planner) coalesce(anchor *Segment) {
	idx := indexOf(p.segments, anchor)
	if idx < 0 {
		return
	}
	start := idx
	end := idx + 1
	if idx > 0 && p.segments[idx-1].Tag == NoTag {
		start = idx - 1
	}
	if idx+1 < len(p.segments) && p.segments[idx+1].Tag == NoTag {
		end = idx + 2
	}
	if end-start <= 1 {
		return
	}
	merged := &Segment{
		Start: p.segments[start].Start,
		End:   p.segments[end-1].End,
		Tag:   NoTag,
	}
	next := make([]*Segment, 0, len(p.segments)-(end-start)+1)
	next = append(next, p.segments[:start]...)
	next = append(next, merged)
	next = append(next, p.segments[end:]...)
	p.segments = next
}

func (p *Planner) replaceSegment(old *Segment, replacement []*Segment) {
	idx := indexOf(p.segments, old)
	if idx < 0 {
		panic("allocplan: internal segment not found")
	}
	next := make([]*Segment, 0, len(p.segments)-1+len(replacement))
	next = append(next, p.segments[:idx]...)
	next = append(next, replacement...)
	next = append(next, p.segments[idx+1:]...)
	p.segments = next
}

func (p *Planner) findStart(start int) *Segment {
	for _, seg := range p.segments {
		if seg.Start == start {
			return seg
		}
	}
	return nil
}

func indexOf(segments []*Segment, target *Segment) int {
	for i, seg := range segments {
		if seg == target {
			return i
		}
	}
	return -1
}

// saturatingAdd returns a+b without wrapping, clamping at MaxInt64 so a
// quarantine far in the future cannot overflow into an already-expired tick.
func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > (1<<63-1)-b {
		return 1<<63 - 1
	}
	return a + b
}
