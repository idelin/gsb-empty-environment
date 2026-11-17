package netsec

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Tick is a virtual-clock instant owned by the memory host.
type Tick uint64

// Label is a finite detection label attached to an allocation.
type Label uint64

// Tag is an alias for Label.
type Tag = Label

// AllocationID identifies one issued allocation.
type AllocationID uint64

// Span is a half-open address interval [Start, End).
type Span struct {
	Start uint64
	End   uint64
}

// Len returns the number of addresses in s.
func (s Span) Len() uint64 {
	if s.End <= s.Start {
		return 0
	}
	return s.End - s.Start
}

// Valid reports whether s is a non-empty, non-wrapping interval.
func (s Span) Valid() bool {
	return s.Start < s.End
}

// Contains reports whether inner is fully inside s.
func (s Span) Contains(inner Span) bool {
	return inner.Valid() && s.Start <= inner.Start && inner.End <= s.End
}

// ExhaustionPolicy selects what a plan does when every label is quarantined.
type ExhaustionPolicy uint8

const (
	// RejectWhenExhausted returns StatusRejected.
	RejectWhenExhausted ExhaustionPolicy = iota
	// WaitWhenExhausted returns StatusWait and the earliest safe retry instant.
	WaitWhenExhausted
)

// Config defines the finite label domain and quarantine duration.
type Config struct {
	LabelCount uint64
	Quarantine Tick
	Exhaustion ExhaustionPolicy
}

// Status is the result of an allocation plan.
type Status uint8

const (
	StatusNone Status = iota
	StatusGranted
	StatusWait
	StatusRejected
)

// Reason explains why an allocation was not granted.
type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonNoSpace
	ReasonQuarantined
)

// Allocation is a granted plan. The host must not write memory unless its
// containing Plan has StatusGranted.
type Allocation struct {
	ID       AllocationID
	Span     Span
	Label    Label
	IssuedAt Tick
}

// Plan is the outcome of an allocation request. RetryAt is meaningful when
// Status is StatusWait, and is also populated on StatusRejected for callers
// that want to retry explicitly later.
type Plan struct {
	Status     Status
	Allocation Allocation
	RetryAt    Tick
	Reason     Reason
}

// Quarantine records that label must not be used in Span before ExpiresAt.
// ExpiresAt is inclusive for availability: a label is reusable at that instant.
type Quarantine struct {
	Span      Span
	Label     Label
	ExpiresAt Tick
}

// SegmentInfo is a read-only snapshot of one planned address segment.
type SegmentInfo struct {
	Span       Span
	Allocated  bool
	Allocation Allocation
	History    []Quarantine
}

// LabelState reports when one label can next be used for a requested span.
type LabelState struct {
	Label       Label
	Available   bool
	AvailableAt Tick
}

var (
	// ErrInvalidConfig reports an invalid finite-label or quarantine policy.
	ErrInvalidConfig = errors.New("netsec: invalid config")
	// ErrInvalidSpan reports a zero-length or wrapping address interval.
	ErrInvalidSpan = errors.New("netsec: invalid span")
	// ErrOverlap reports an address interval that overlaps a planned segment.
	ErrOverlap = errors.New("netsec: overlapping segment")
	// ErrNotFree reports an operation that requires a single free segment.
	ErrNotFree = errors.New("netsec: span is not free")
	// ErrNotAdjacent reports a merge of non-adjacent segments.
	ErrNotAdjacent = errors.New("netsec: segments are not adjacent")
	// ErrAllocationNotFound reports an unknown or already released allocation ID.
	ErrAllocationNotFound = errors.New("netsec: allocation not found")
	// ErrAllocationMismatch reports release details that do not match the active allocation.
	ErrAllocationMismatch = errors.New("netsec: allocation details do not match")
	// ErrClockRegression reports a virtual-clock instant older than one already observed.
	ErrClockRegression = errors.New("netsec: virtual clock moved backwards")
	// ErrTimeOverflow reports a quarantine expiry that cannot be represented.
	ErrTimeOverflow = errors.New("netsec: quarantine expiry overflows Tick")
	// ErrIDExhausted reports that no further allocation ID can be issued.
	ErrIDExhausted = errors.New("netsec: allocation ID exhausted")
)

type segment struct {
	span       Span
	allocated  bool
	allocation Allocation
	history    []Quarantine
}

// Planner plans labelled allocations over registered address segments. It is
// safe for concurrent method calls, although the host should still serialize
// decisions made from separate plans.
type Planner struct {
	mu sync.Mutex

	cfg      Config
	segments []segment

	clockSeen bool
	now       Tick
	nextID    AllocationID
}

// Allocator is an alias for Planner.
type Allocator = Planner

// AllocatorConfig is an alias for Config.
type AllocatorConfig = Config

// New constructs an allocation planner.
func New(cfg Config) (*Planner, error) {
	if cfg.LabelCount == 0 {
		return nil, fmt.Errorf("%w: label count must be positive", ErrInvalidConfig)
	}
	if cfg.Quarantine == 0 {
		return nil, fmt.Errorf("%w: quarantine must be positive", ErrInvalidConfig)
	}
	if cfg.Exhaustion != RejectWhenExhausted && cfg.Exhaustion != WaitWhenExhausted {
		return nil, fmt.Errorf("%w: unknown exhaustion policy %d", ErrInvalidConfig, cfg.Exhaustion)
	}
	return &Planner{cfg: cfg}, nil
}

// NewPlanner is an alias for New.
func NewPlanner(cfg Config) (*Planner, error) {
	return New(cfg)
}

// NewAllocator is an alias for New.
func NewAllocator(cfg Config) (*Planner, error) {
	return New(cfg)
}

// Config returns a copy of the planner configuration.
func (p *Planner) Config() Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg
}

// AddFree registers a free address interval. It merges adjacent free segments
// without discarding any quarantine history.
func (p *Planner) AddFree(span Span) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !span.Valid() {
		return ErrInvalidSpan
	}
	for _, seg := range p.segments {
		if spansOverlap(span, seg.span) {
			return fmt.Errorf("%w: [%d,%d) overlaps [%d,%d)",
				ErrOverlap, span.Start, span.End, seg.span.Start, seg.span.End)
		}
	}
	p.segments = append(p.segments, segment{span: span})
	sortSegments(p.segments)
	p.mergeAdjacentLocked()
	return nil
}

// Add is an alias for AddFree.
func (p *Planner) Add(span Span) error {
	return p.AddFree(span)
}

// Allocate plans an allocation at exactly span. It never reuses a label while
// that label is quarantined over any address in span.
func (p *Planner) Allocate(now Tick, span Span) (Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !span.Valid() {
		return Plan{}, ErrInvalidSpan
	}
	if err := p.observeLocked(now); err != nil {
		return Plan{}, err
	}

	lo, hi, ok := p.freeRunLocked(span)
	if !ok {
		return Plan{
			Status: StatusRejected,
			Reason: ReasonNoSpace,
		}, nil
	}

	history := p.historyForRunLocked(lo, hi)
	label, available, retryAt := pickLabel(history, span, now, p.cfg.LabelCount)
	if !available {
		status := StatusRejected
		if p.cfg.Exhaustion == WaitWhenExhausted {
			status = StatusWait
		}
		return Plan{
			Status:  status,
			RetryAt: retryAt,
			Reason:  ReasonQuarantined,
		}, nil
	}

	if p.nextID == AllocationID(math.MaxUint64) {
		return Plan{}, ErrIDExhausted
	}
	allocation := Allocation{
		ID:       p.nextID + 1,
		Span:     span,
		Label:    label,
		IssuedAt: now,
	}
	p.carveLocked(lo, hi, allocation, history)
	p.nextID = allocation.ID
	return Plan{
		Status:     StatusGranted,
		Allocation: allocation,
	}, nil
}

// AllocateFirstFit plans the first address interval that can satisfy size.
// It considers every free interval before returning exhaustion, so a later
// segment with an available label is preferred over waiting on an earlier one.
func (p *Planner) AllocateFirstFit(now Tick, size uint64) (Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if size == 0 {
		return Plan{}, ErrInvalidSpan
	}
	if err := p.observeLocked(now); err != nil {
		return Plan{}, err
	}

	exhausted := false
	var retryAt Tick
	for i := range p.segments {
		seg := p.segments[i]
		if seg.allocated || seg.span.Len() < size {
			continue
		}
		if size > math.MaxUint64-seg.span.Start {
			continue
		}
		span := Span{Start: seg.span.Start, End: seg.span.Start + size}
		label, available, candidateRetry := pickLabel(seg.history, span, now, p.cfg.LabelCount)
		if available {
			if p.nextID == AllocationID(math.MaxUint64) {
				return Plan{}, ErrIDExhausted
			}
			allocation := Allocation{
				ID:       p.nextID + 1,
				Span:     span,
				Label:    label,
				IssuedAt: now,
			}
			p.carveLocked(i, i, allocation, seg.history)
			p.nextID = allocation.ID
			return Plan{
				Status:     StatusGranted,
				Allocation: allocation,
			}, nil
		}
		if !exhausted || candidateRetry < retryAt {
			retryAt = candidateRetry
		}
		exhausted = true
	}

	if exhausted {
		status := StatusRejected
		if p.cfg.Exhaustion == WaitWhenExhausted {
			status = StatusWait
		}
		return Plan{
			Status:  status,
			RetryAt: retryAt,
			Reason:  ReasonQuarantined,
		}, nil
	}
	return Plan{
		Status: StatusRejected,
		Reason: ReasonNoSpace,
	}, nil
}

// AllocateAt is a convenience form of Allocate.
func (p *Planner) AllocateAt(now Tick, start, end uint64) (Plan, error) {
	return p.Allocate(now, Span{Start: start, End: end})
}

// Release frees an active allocation and starts its label quarantine. ID and
// allocation details are checked so stale handles cannot free a newer plan.
func (p *Planner) Release(now Tick, allocation Allocation) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.observeLocked(now); err != nil {
		return err
	}
	expiresAt, err := addTicksChecked(now, p.cfg.Quarantine)
	if err != nil {
		return err
	}

	index := -1
	for i := range p.segments {
		if p.segments[i].allocated && p.segments[i].allocation.ID == allocation.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%w: %d", ErrAllocationNotFound, allocation.ID)
	}
	seg := &p.segments[index]
	if seg.allocation.Span != allocation.Span ||
		seg.allocation.Label != allocation.Label ||
		seg.allocation.IssuedAt != allocation.IssuedAt {
		return ErrAllocationMismatch
	}

	seg.history = append(seg.history, Quarantine{
		Span:      seg.span,
		Label:     seg.allocation.Label,
		ExpiresAt: expiresAt,
	})
	seg.history = compactHistory(seg.history)
	seg.allocated = false
	seg.allocation = Allocation{}
	p.mergeAdjacentLocked()
	return nil
}

// ReleaseID releases by allocation ID.
func (p *Planner) ReleaseID(now Tick, id AllocationID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.observeLocked(now); err != nil {
		return err
	}
	expiresAt, err := addTicksChecked(now, p.cfg.Quarantine)
	if err != nil {
		return err
	}
	for i := range p.segments {
		if !p.segments[i].allocated || p.segments[i].allocation.ID != id {
			continue
		}
		seg := &p.segments[i]
		seg.history = append(seg.history, Quarantine{
			Span:      seg.span,
			Label:     seg.allocation.Label,
			ExpiresAt: expiresAt,
		})
		seg.history = compactHistory(seg.history)
		seg.allocated = false
		seg.allocation = Allocation{}
		p.mergeAdjacentLocked()
		return nil
	}
	return fmt.Errorf("%w: %d", ErrAllocationNotFound, id)
}

// Split divides one free segment at at. Both children retain the history that
// overlaps their new address ranges.
func (p *Planner) Split(span Span, at uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !span.Valid() || at <= span.Start || at >= span.End {
		return ErrInvalidSpan
	}
	index := p.findExactLocked(span)
	if index < 0 || p.segments[index].allocated {
		return fmt.Errorf("%w: [%d,%d)", ErrNotFree, span.Start, span.End)
	}

	left := segment{
		span:    Span{Start: span.Start, End: at},
		history: clipHistory(p.segments[index].history, Span{Start: span.Start, End: at}),
	}
	right := segment{
		span:    Span{Start: at, End: span.End},
		history: clipHistory(p.segments[index].history, Span{Start: at, End: span.End}),
	}
	replacement := []segment{left, right}
	newSegments := make([]segment, 0, len(p.segments)+1)
	newSegments = append(newSegments, p.segments[:index]...)
	newSegments = append(newSegments, replacement...)
	newSegments = append(newSegments, p.segments[index+1:]...)
	p.segments = newSegments
	return nil
}

// Merge joins two adjacent free segments. History from both sides is retained.
func (p *Planner) Merge(left, right Span) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !left.Valid() || !right.Valid() {
		return ErrInvalidSpan
	}
	if right.Start == left.End {
	} else if left.Start == right.End {
		left, right = right, left
	} else {
		return ErrNotAdjacent
	}
	leftIndex := p.findExactLocked(left)
	rightIndex := p.findExactLocked(right)
	if leftIndex < 0 || rightIndex < 0 ||
		p.segments[leftIndex].allocated || p.segments[rightIndex].allocated {
		return fmt.Errorf("%w: [%d,%d) or [%d,%d)",
			ErrNotFree, left.Start, left.End, right.Start, right.End)
	}

	merged := segment{
		span: Span{Start: left.Start, End: right.End},
		history: compactHistory(append(
			append([]Quarantine(nil), p.segments[leftIndex].history...),
			p.segments[rightIndex].history...,
		)),
	}
	removeHigh := rightIndex
	removeLow := leftIndex
	if removeHigh < removeLow {
		removeHigh, removeLow = removeLow, removeHigh
	}
	newSegments := make([]segment, 0, len(p.segments)-1)
	newSegments = append(newSegments, p.segments[:removeLow]...)
	newSegments = append(newSegments, merged)
	newSegments = append(newSegments, p.segments[removeHigh+1:]...)
	sortSegments(newSegments)
	p.segments = newSegments
	p.mergeAdjacentLocked()
	return nil
}

// Segments returns snapshots in address order.
func (p *Planner) Segments() []SegmentInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]SegmentInfo, len(p.segments))
	for i, seg := range p.segments {
		out[i] = SegmentInfo{
			Span:       seg.span,
			Allocated:  seg.allocated,
			Allocation: seg.allocation,
			History:    append([]Quarantine(nil), seg.history...),
		}
	}
	return out
}

// LabelStates reports every label's next availability for a free span.
func (p *Planner) LabelStates(now Tick, span Span) ([]LabelState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !span.Valid() {
		return nil, ErrInvalidSpan
	}
	if err := p.observeLocked(now); err != nil {
		return nil, err
	}
	lo, hi, ok := p.freeRunLocked(span)
	if !ok {
		return nil, fmt.Errorf("%w: [%d,%d)", ErrNotFree, span.Start, span.End)
	}
	history := p.historyForRunLocked(lo, hi)
	states := make([]LabelState, 0, p.cfg.LabelCount)
	for i := uint64(0); i < p.cfg.LabelCount; i++ {
		label := Label(i)
		availableAt := maxExpiry(history, span, label)
		state := LabelState{
			Label:       label,
			AvailableAt: availableAt,
		}
		if availableAt <= now {
			state.Available = true
			state.AvailableAt = now
		}
		states = append(states, state)
	}
	return states, nil
}

// Now returns the latest virtual-clock instant observed by timestamped methods.
func (p *Planner) Now() (Tick, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now, p.clockSeen
}

func (p *Planner) observeLocked(now Tick) error {
	if p.clockSeen && now < p.now {
		return fmt.Errorf("%w: %d after %d", ErrClockRegression, now, p.now)
	}
	p.clockSeen = true
	if now > p.now {
		p.now = now
	}
	return nil
}

func (p *Planner) freeRunLocked(span Span) (int, int, bool) {
	lo := sort.Search(len(p.segments), func(i int) bool {
		return p.segments[i].span.End > span.Start
	})
	if lo == len(p.segments) {
		return 0, 0, false
	}
	first := p.segments[lo]
	if first.allocated || first.span.Start > span.Start {
		return 0, 0, false
	}
	if span.End <= first.span.End {
		return lo, lo, true
	}

	cursor := first.span.End
	hi := lo
	for cursor < span.End {
		hi++
		if hi == len(p.segments) {
			return 0, 0, false
		}
		next := p.segments[hi]
		if next.allocated || next.span.Start != cursor {
			return 0, 0, false
		}
		cursor = next.span.End
	}
	return lo, hi, true
}

func (p *Planner) historyForRunLocked(lo, hi int) []Quarantine {
	var history []Quarantine
	for i := lo; i <= hi; i++ {
		history = append(history, p.segments[i].history...)
	}
	return compactHistory(history)
}

func (p *Planner) carveLocked(lo, hi int, allocation Allocation, history []Quarantine) {
	first := p.segments[lo]
	last := p.segments[hi]
	replacement := make([]segment, 0, 3)

	if first.span.Start < allocation.Span.Start {
		prefix := Span{Start: first.span.Start, End: allocation.Span.Start}
		replacement = append(replacement, segment{
			span:    prefix,
			history: clipHistory(first.history, prefix),
		})
	}
	replacement = append(replacement, segment{
		span:       allocation.Span,
		allocated:  true,
		allocation: allocation,
		history:    clipHistory(history, allocation.Span),
	})
	if allocation.Span.End < last.span.End {
		suffix := Span{Start: allocation.Span.End, End: last.span.End}
		replacement = append(replacement, segment{
			span:    suffix,
			history: clipHistory(last.history, suffix),
		})
	}

	newSegments := make([]segment, 0, len(p.segments)-(hi-lo+1)+len(replacement))
	newSegments = append(newSegments, p.segments[:lo]...)
	newSegments = append(newSegments, replacement...)
	newSegments = append(newSegments, p.segments[hi+1:]...)
	p.segments = newSegments
}

func (p *Planner) mergeAdjacentLocked() {
	if len(p.segments) < 2 {
		return
	}
	out := make([]segment, 0, len(p.segments))
	for _, seg := range p.segments {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if !prev.allocated && !seg.allocated && prev.span.End == seg.span.Start {
				prev.span.End = seg.span.End
				prev.history = compactHistory(append(prev.history, seg.history...))
				continue
			}
		}
		out = append(out, seg)
	}
	p.segments = out
}

func (p *Planner) findExactLocked(span Span) int {
	index := sort.Search(len(p.segments), func(i int) bool {
		return p.segments[i].span.End > span.Start
	})
	if index < len(p.segments) && p.segments[index].span == span {
		return index
	}
	return -1
}

func pickLabel(history []Quarantine, span Span, now Tick, count uint64) (Label, bool, Tick) {
	var retryAt Tick
	for i := uint64(0); i < count; i++ {
		label := Label(i)
		expiresAt := maxExpiry(history, span, label)
		if expiresAt <= now {
			return label, true, 0
		}
		if retryAt == 0 || expiresAt < retryAt {
			retryAt = expiresAt
		}
	}
	return 0, false, retryAt
}

func maxExpiry(history []Quarantine, span Span, label Label) Tick {
	var latest Tick
	for _, record := range history {
		if record.Label != label || !spansOverlap(record.Span, span) {
			continue
		}
		if record.ExpiresAt > latest {
			latest = record.ExpiresAt
		}
	}
	return latest
}

func clipHistory(history []Quarantine, span Span) []Quarantine {
	out := make([]Quarantine, 0, len(history))
	for _, record := range history {
		clipped, ok := intersect(record.Span, span)
		if !ok {
			continue
		}
		record.Span = clipped
		out = append(out, record)
	}
	return compactHistory(out)
}

func compactHistory(history []Quarantine) []Quarantine {
	if len(history) == 0 {
		return nil
	}
	out := append([]Quarantine(nil), history...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		if a.Span.End != b.Span.End {
			return a.Span.End < b.Span.End
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ExpiresAt < b.ExpiresAt
	})

	compacted := make([]Quarantine, 0, len(out))
	for _, record := range out {
		if len(compacted) > 0 {
			prev := &compacted[len(compacted)-1]
			if prev.Label == record.Label &&
				prev.ExpiresAt == record.ExpiresAt &&
				record.Span.Start <= prev.Span.End {
				if record.Span.End > prev.Span.End {
					prev.Span.End = record.Span.End
				}
				continue
			}
		}
		compacted = append(compacted, record)
	}
	return compacted
}

func sortSegments(segments []segment) {
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].span.Start < segments[j].span.Start
	})
}

func spansOverlap(a, b Span) bool {
	return a.Start < b.End && b.Start < a.End
}

func intersect(a, b Span) (Span, bool) {
	start := a.Start
	if b.Start > start {
		start = b.Start
	}
	end := a.End
	if b.End < end {
		end = b.End
	}
	if start >= end {
		return Span{}, false
	}
	return Span{Start: start, End: end}, true
}

func addTicksChecked(a, b Tick) (Tick, error) {
	if uint64(b) > math.MaxUint64-uint64(a) {
		return 0, ErrTimeOverflow
	}
	return a + b, nil
}
