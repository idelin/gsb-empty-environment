package netsec

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Detector consumes JNI trace events and reports critical-section violations.
//
// A Detector is safe for concurrent use. Events from different threads may be
// recorded concurrently. Findings are retained and are also delivered to
// subscribers in the order in which the corresponding event was recorded.
type Detector struct {
	mu       sync.Mutex
	threads  map[ThreadID]*threadState
	findings []Finding
	subs     []*subscriber
	nextSub  uint64
	now      func() time.Time
}

type subscriber struct {
	id uint64
	fn func(Finding)
}

type threadState struct {
	active   []*criticalEntry
	released []*releasedRange
	nextSeq  uint64
}

type criticalEntry struct {
	seq        uint64
	array      ArrayID
	base       uintptr
	length     int64
	elementSz  int64
	bytes      int64
	writes     int
	writeBytes int64
}

type releasedRange struct {
	seq       uint64
	array     ArrayID
	base      uintptr
	length    int64
	elementSz int64
	bytes     int64
	mode      ReleaseMode
}

// Option configures a Detector.
type Option func(*Detector)

// WithSubscriber registers a subscriber before the detector is returned.
func WithSubscriber(fn func(Finding)) Option {
	return func(d *Detector) {
		if fn != nil {
			d.addSubscriber(fn)
		}
	}
}

// WithClock replaces the clock used for finding timestamps. It is primarily
// useful in deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(d *Detector) {
		if now != nil {
			d.now = now
		}
	}
}

// NewDetector creates an empty detector.
func NewDetector(opts ...Option) *Detector {
	d := &Detector{
		threads: make(map[ThreadID]*threadState),
		now:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(d)
		}
	}
	return d
}

// New is an alias for NewDetector.
func New(opts ...Option) *Detector {
	return NewDetector(opts...)
}

// Record processes one event and returns the findings produced by it.
func (d *Detector) Record(ev Event) []Finding {
	at := ev.At
	if at.IsZero() {
		at = d.now()
	}

	d.mu.Lock()
	findings := d.recordLocked(ev, at)
	if len(findings) > 0 {
		d.findings = append(d.findings, findings...)
	}
	subs := d.snapshotSubscribersLocked()
	d.mu.Unlock()

	for _, f := range findings {
		for _, sub := range subs {
			sub.fn(f)
		}
	}
	return findings
}

// Feed records several events in order and returns all new findings.
func (d *Detector) Feed(events ...Event) []Finding {
	var all []Finding
	for _, ev := range events {
		all = append(all, d.Record(ev)...)
	}
	return all
}

// ThreadEnded is a convenience wrapper for recording EventThreadEnd.
func (d *Detector) ThreadEnded(thread ThreadID) []Finding {
	return d.Record(ThreadEndEvent(thread))
}

// Findings returns a copy of all findings accumulated so far.
func (d *Detector) Findings() []Finding {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]Finding, len(d.findings))
	copy(out, d.findings)
	return out
}

// Reset clears all thread state and findings. Subscribers are retained.
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.threads = make(map[ThreadID]*threadState)
	d.findings = nil
}

// Subscribe registers fn to receive findings as they are detected. The
// returned function unsubscribes fn and is safe to call more than once.
func (d *Detector) Subscribe(fn func(Finding)) func() {
	if fn == nil {
		return func() {}
	}

	d.mu.Lock()
	id := d.addSubscriberLocked(fn)
	d.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			for i, sub := range d.subs {
				if sub.id == id {
					d.subs = append(d.subs[:i], d.subs[i+1:]...)
					break
				}
			}
		})
	}
}

func (d *Detector) addSubscriber(fn func(Finding)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addSubscriberLocked(fn)
}

func (d *Detector) addSubscriberLocked(fn func(Finding)) uint64 {
	d.nextSub++
	id := d.nextSub
	d.subs = append(d.subs, &subscriber{id: id, fn: fn})
	return id
}

func (d *Detector) snapshotSubscribersLocked() []*subscriber {
	out := make([]*subscriber, len(d.subs))
	copy(out, d.subs)
	return out
}

func (d *Detector) recordLocked(ev Event, at time.Time) []Finding {
	ts := d.threads[ev.Thread]
	if ts == nil {
		ts = &threadState{}
		d.threads[ev.Thread] = ts
	}

	switch ev.Kind {
	case EventGet:
		ts.active = append(ts.active, ts.newEntry(ev, at))
		return nil

	case EventRelease:
		return ts.release(ev, at)

	case EventJNICall:
		if len(ts.active) == 0 || isAllowedInCritical(ev.Function) {
			return nil
		}
		return []Finding{{
			Kind:     FindingJNICallInCritical,
			Severity: SeverityViolation,
			Thread:   ev.Thread,
			Function: ev.Function,
			Message: fmt.Sprintf(
				"JNI function %q called while thread %q holds a critical array pointer",
				ev.Function, ev.Thread),
			At: at,
		}}

	case EventBlockingCall:
		if len(ts.active) == 0 {
			return nil
		}
		return []Finding{{
			Kind:     FindingBlockingInCritical,
			Severity: SeverityViolation,
			Thread:   ev.Thread,
			Function: ev.Function,
			Message: fmt.Sprintf(
				"blocking call %q made while thread %q holds a critical array pointer",
				ev.Function, ev.Thread),
			At: at,
		}}

	case EventRead, EventWrite:
		return ts.access(ev, at)

	case EventThreadEnd:
		return ts.threadEnd(ev, at)

	default:
		return nil
	}
}

func (ts *threadState) newEntry(ev Event, at time.Time) *criticalEntry {
	ts.nextSeq++
	elementSz := ev.elementSize()
	length := ev.Length
	if length < 0 {
		length = 0
	}
	return &criticalEntry{
		seq:       ts.nextSeq,
		array:     ev.Array,
		base:      ev.Address,
		length:    length,
		elementSz: elementSz,
		bytes:     arrayBytes(length, elementSz),
	}
}

func (ts *threadState) release(ev Event, at time.Time) []Finding {
	idx := ts.findActiveForRelease(ev)
	if idx < 0 {
		if prev := ts.findReleased(ev); prev != nil {
			array := ev.Array
			if array == "" {
				array = prev.array
			}
			address := ev.Address
			if address == 0 {
				address = prev.base
			}
			length := ev.Length
			if length <= 0 {
				length = prev.length
			}
			return []Finding{{
				Kind:        FindingDuplicateRelease,
				Severity:    SeverityViolation,
				Thread:      ev.Thread,
				Array:       array,
				Address:     address,
				Length:      length,
				ArrayLength: prev.length,
				ElementSize: prev.elementSz,
				ArrayBytes:  prev.bytes,
				Mode:        ev.Mode,
				Message: fmt.Sprintf(
					"array %q released again after its critical section already ended (thread %q)",
					array, ev.Thread),
				At: at,
			}}
		}
		return []Finding{{
			Kind:     FindingInvalidRelease,
			Severity: SeverityWarning,
			Thread:   ev.Thread,
			Array:    ev.Array,
			Address:  ev.Address,
			Length:   ev.Length,
			Mode:     ev.Mode,
			Message: fmt.Sprintf(
				"release of array %q does not match any active critical section (thread %q)",
				ev.Array, ev.Thread),
			At: at,
		}}
	}

	entry := ts.active[idx]
	if ev.Mode == JNI_COMMIT {
		// A commit copies pending writes back but keeps the critical section
		// active. Only writes made after the commit remain uncommitted.
		entry.writes = 0
		entry.writeBytes = 0
		return nil
	}

	ts.active = append(ts.active[:idx], ts.active[idx+1:]...)
	ts.released = append(ts.released, &releasedRange{
		seq:       entry.seq,
		array:     entry.array,
		base:      entry.base,
		length:    entry.length,
		elementSz: entry.elementSz,
		bytes:     entry.bytes,
		mode:      ev.Mode,
	})

	if ev.Mode == JNI_ABORT && entry.writes > 0 {
		return []Finding{{
			Kind:        FindingWritesDiscarded,
			Severity:    SeverityWarning,
			Thread:      ev.Thread,
			Array:       entry.array,
			Address:     entry.base,
			Length:      entry.length,
			ArrayLength: entry.length,
			ElementSize: entry.elementSz,
			ArrayBytes:  entry.bytes,
			Mode:        ev.Mode,
			Writes:      entry.writes,
			WriteBytes:  entry.writeBytes,
			Message: fmt.Sprintf(
				"JNI_ABORT discarded %d write(s) (%d byte(s)) made through the critical pointer of array %q",
				entry.writes, entry.writeBytes, entry.array),
			At: at,
		}}
	}
	return nil
}

func (ts *threadState) findActiveForRelease(ev Event) int {
	if ev.Array != "" {
		for i := len(ts.active) - 1; i >= 0; i-- {
			if ts.active[i].array == ev.Array {
				return i
			}
		}
	}
	if ev.Address != 0 {
		for i := len(ts.active) - 1; i >= 0; i-- {
			if ts.active[i].base == ev.Address {
				return i
			}
		}
	}
	if ev.Array == "" && ev.Address == 0 && len(ts.active) > 0 {
		return len(ts.active) - 1
	}
	return -1
}

func (ts *threadState) findReleased(ev Event) *releasedRange {
	for i := len(ts.released) - 1; i >= 0; i-- {
		rel := ts.released[i]
		if ev.Array != "" {
			if rel.array == ev.Array {
				return rel
			}
			continue
		}
		if ev.Address != 0 && rel.base == ev.Address {
			return rel
		}
	}
	return nil
}

func (ts *threadState) access(ev Event, at time.Time) []Finding {
	size := ev.accessSize()
	if size < 0 {
		size = 0
	}

	entry := ts.findActiveForAccess(ev)
	if entry == nil {
		if rel := ts.findReleasedForAccess(ev, size); rel != nil {
			offset, _ := addressOffset(ev.Address, rel.base)
			action := "read"
			if ev.Kind == EventWrite {
				action = "write"
			}
			return []Finding{{
				Kind:        FindingUseAfterRelease,
				Severity:    SeverityViolation,
				Thread:      ev.Thread,
				Array:       rel.array,
				Address:     ev.Address,
				Length:      size,
				Offset:      offset,
				ArrayLength: rel.length,
				ElementSize: rel.elementSz,
				ArrayBytes:  rel.bytes,
				Mode:        rel.mode,
				Message: fmt.Sprintf(
					"%s of %d byte(s) through pointer of array %q after it was released with mode %s",
					action, size, rel.array, rel.mode),
				At: at,
			}}
		}
		if len(ts.active) == 1 {
			// With a single active pointer, attribute clearly out-of-range
			// accesses to it so large overflows are still visible.
			entry = ts.active[0]
		}
	}

	if entry != nil {
		offset, ok := addressOffset(ev.Address, entry.base)
		addressOverflow := size > 0 && uint64(^uintptr(0)-ev.Address) < uint64(size)
		if !ok || addressOverflow || offset < 0 || offset > entry.bytes || size > entry.bytes-offset {
			kind := FindingOutOfBoundsRead
			action := "read"
			if ev.Kind == EventWrite {
				kind = FindingOutOfBoundsWrite
				action = "write"
			}
			return []Finding{{
				Kind:        kind,
				Severity:    SeverityViolation,
				Thread:      ev.Thread,
				Array:       entry.array,
				Address:     ev.Address,
				Length:      size,
				Offset:      offset,
				ArrayLength: entry.length,
				ElementSize: entry.elementSz,
				ArrayBytes:  entry.bytes,
				Message: fmt.Sprintf(
					"%s of %d byte(s) at offset %d exceeds array %q (%d element(s), %d byte(s))",
					action, size, offset, entry.array, entry.length, entry.bytes),
				At: at,
			}}
		}

		if ev.Kind == EventWrite && size > 0 {
			entry.writes++
			entry.writeBytes = addInt64Saturated(entry.writeBytes, size)
		}
		return nil
	}
	return nil
}

// findActiveForAccess returns the critical pointer that an access belongs to.
// Explicitly tagged accesses win. Otherwise a pointer range containing the
// access start (including its one-past-the-end address) is selected.
func (ts *threadState) findActiveForAccess(ev Event) *criticalEntry {
	if ev.Array != "" {
		for i := len(ts.active) - 1; i >= 0; i-- {
			if ts.active[i].array == ev.Array {
				return ts.active[i]
			}
		}
	}

	var best *criticalEntry
	for _, entry := range ts.active {
		if accessTouchesEntry(ev.Address, ev.accessSize(), entry) {
			if best == nil || entry.seq > best.seq {
				best = entry
			}
		}
	}
	if best != nil {
		return best
	}
	return nil
}

func accessTouchesEntry(address uintptr, size int64, entry *criticalEntry) bool {
	if entry.bytes == 0 {
		return address == entry.base
	}
	offset, ok := addressOffset(address, entry.base)
	if ok && offset >= 0 && offset <= entry.bytes {
		return true
	}
	// Catch an access that begins before the pointer but overlaps its start.
	if ok && offset < 0 && size > 0 && size > -offset {
		return true
	}
	return false
}

func (ts *threadState) findReleasedForAccess(ev Event, size int64) *releasedRange {
	for i := len(ts.released) - 1; i >= 0; i-- {
		rel := ts.released[i]
		if ev.Array != "" && rel.array == ev.Array {
			return rel
		}
		if spansOverlap(ev.Address, size, rel.base, rel.bytes) {
			return rel
		}
	}
	return nil
}

func (ts *threadState) threadEnd(ev Event, at time.Time) []Finding {
	if len(ts.active) == 0 {
		return nil
	}

	findings := make([]Finding, 0, len(ts.active))
	for _, entry := range ts.active {
		findings = append(findings, Finding{
			Kind:        FindingUnreleasedAtThreadEnd,
			Severity:    SeverityViolation,
			Thread:      ev.Thread,
			Array:       entry.array,
			Address:     entry.base,
			Length:      entry.length,
			ArrayLength: entry.length,
			ElementSize: entry.elementSz,
			ArrayBytes:  entry.bytes,
			Message: fmt.Sprintf(
				"thread %q ended while array %q was still held in a critical section",
				ev.Thread, entry.array),
			At: at,
		})

		ts.released = append(ts.released, &releasedRange{
			seq:       entry.seq,
			array:     entry.array,
			base:      entry.base,
			length:    entry.length,
			elementSz: entry.elementSz,
			bytes:     entry.bytes,
			mode:      ReleaseModeDefault,
		})
	}
	ts.active = nil
	return findings
}

func arrayBytes(length, elementSize int64) int64 {
	if length <= 0 || elementSize <= 0 {
		return 0
	}
	if length > math.MaxInt64/elementSize {
		return math.MaxInt64
	}
	return length * elementSize
}

func addInt64Saturated(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func addressOffset(address, base uintptr) (int64, bool) {
	if address >= base {
		diff := address - base
		if diff > math.MaxInt64 {
			return 0, false
		}
		return int64(diff), true
	}
	diff := base - address
	if diff > math.MaxInt64 {
		return 0, false
	}
	return -int64(diff), true
}

func spansOverlap(address uintptr, size int64, base uintptr, bytes int64) bool {
	if size <= 0 || bytes <= 0 {
		return false
	}
	accessEnd := saturatingAdd(address, uint64(size))
	rangeEnd := saturatingAdd(base, uint64(bytes))
	return address < rangeEnd && base < accessEnd
}

func saturatingAdd(address uintptr, n uint64) uintptr {
	if ^uintptr(0)-address < uintptr(n) {
		return ^uintptr(0)
	}
	return address + uintptr(n)
}
