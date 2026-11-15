package netsec

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxReleasedSpansPerThread = 4096

type activeRegion struct {
	array       ArrayID
	pointer     uintptr
	length      int
	elementSize int
	getSeq      uint64

	uncommittedWrites int
	uncommittedBytes  int64
}

type releaseRecord struct {
	array       ArrayID
	pointer     uintptr
	length      int
	elementSize int
	getSeq      uint64
	releaseSeq  uint64
	mode        ReleaseMode
}

type threadState struct {
	active           []*activeRegion
	releases         map[ArrayID]releaseRecord
	releasedSinceGet map[ArrayID]bool
	releasedSpans    []releaseRecord
}

type eventSubscription struct {
	id uint64
	fn func(Event)
}

type findingSubscription struct {
	id uint64
	fn func(Finding)
}

// Detector consumes JNI trace events and reports critical-section violations.
// It is safe for concurrent use. Callers normally use the convenience methods
// (Get, Release, JNICall, BlockingCall, Read, Write, and ThreadExit), or feed a
// raw stream of Event values through Observe.
//
// The zero value is ready for use.
type Detector struct {
	mu       sync.Mutex
	seq      uint64
	threads  map[ThreadID]*threadState
	findings []Finding

	subMu              sync.RWMutex
	nextSubscription   uint64
	eventSubscribers   []eventSubscription
	findingSubscribers []findingSubscription
}

// NewDetector returns an initialized Detector.
func NewDetector() *Detector {
	return &Detector{threads: make(map[ThreadID]*threadState)}
}

// Get records a successful GetPrimitiveArrayCritical (or another Get*Critical)
// call. length is the array length in elements and elementSize is the number of
// bytes in each element. A non-positive elementSize is treated as one byte.
func (d *Detector) Get(thread ThreadID, array ArrayID, pointer uintptr, length, elementSize int) []Finding {
	return d.Observe(Event{
		Kind:        EventGetCritical,
		Thread:      thread,
		Array:       array,
		ArrayLength: length,
		ElementSize: elementSize,
		Address:     pointer,
	})
}

// Release records ReleasePrimitiveArrayCritical.
func (d *Detector) Release(thread ThreadID, array ArrayID, mode ReleaseMode) []Finding {
	return d.Observe(Event{
		Kind:   EventReleaseCritical,
		Thread: thread,
		Array:  array,
		Mode:   mode,
	})
}

// ReleasePointer is like Release but supplies the pointer being released.
// Supplying the pointer disambiguates nested Get calls for the same array.
func (d *Detector) ReleasePointer(thread ThreadID, array ArrayID, pointer uintptr, mode ReleaseMode) []Finding {
	return d.Observe(Event{
		Kind:    EventReleaseCritical,
		Thread:  thread,
		Array:   array,
		Address: pointer,
		Mode:    mode,
	})
}

// JNICall records an arbitrary JNI function invocation by a thread.
func (d *Detector) JNICall(thread ThreadID, function string) []Finding {
	return d.Observe(Event{Kind: EventJNICall, Thread: thread, Function: function})
}

// BlockingCall records a call that may block the thread.
func (d *Detector) BlockingCall(thread ThreadID, function string) []Finding {
	return d.Observe(Event{Kind: EventBlockingCall, Thread: thread, Function: function})
}

// BlockingCallDuration records a blocking call together with its duration.
func (d *Detector) BlockingCallDuration(thread ThreadID, function string, duration time.Duration) []Finding {
	return d.Observe(Event{
		Kind:     EventBlockingCall,
		Thread:   thread,
		Function: function,
		Duration: duration,
	})
}

// Read records a read through a pointer returned by GetPrimitiveArrayCritical.
// address and length are byte-oriented.
func (d *Detector) Read(thread ThreadID, address uintptr, length int) []Finding {
	return d.Observe(Event{
		Kind:    EventPointerRead,
		Thread:  thread,
		Address: address,
		Length:  length,
	})
}

// ReadArray is like Read and additionally names the array being accessed. It is
// useful when a thread has nested critical regions.
func (d *Detector) ReadArray(thread ThreadID, array ArrayID, address uintptr, length int) []Finding {
	return d.Observe(Event{
		Kind:    EventPointerRead,
		Thread:  thread,
		Array:   array,
		Address: address,
		Length:  length,
	})
}

// Write records a write through a pointer returned by GetPrimitiveArrayCritical.
// address and length are byte-oriented.
func (d *Detector) Write(thread ThreadID, address uintptr, length int) []Finding {
	return d.Observe(Event{
		Kind:    EventPointerWrite,
		Thread:  thread,
		Address: address,
		Length:  length,
	})
}

// WriteArray is like Write and additionally names the array being accessed. It
// is useful when a thread has nested critical regions.
func (d *Detector) WriteArray(thread ThreadID, array ArrayID, address uintptr, length int) []Finding {
	return d.Observe(Event{
		Kind:    EventPointerWrite,
		Thread:  thread,
		Array:   array,
		Address: address,
		Length:  length,
	})
}

// ThreadExit records native thread termination and reports every critical
// region that is still pinned by that thread.
func (d *Detector) ThreadExit(thread ThreadID) []Finding {
	return d.Observe(Event{Kind: EventThreadExit, Thread: thread})
}

// ExitThread is an alias for ThreadExit.
func (d *Detector) ExitThread(thread ThreadID) []Finding {
	return d.ThreadExit(thread)
}

// Observe applies one trace event, updates detector state, delivers the
// normalized event to subscribers, and returns the findings caused by the
// event. Seq, Time, ElementSize, and CriticalDepth are filled in or normalized
// by Observe.
func (d *Detector) Observe(ev Event) []Finding {
	d.mu.Lock()
	d.initLocked()
	d.seq++
	ev.Seq = d.seq
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	if ev.ElementSize <= 0 {
		ev.ElementSize = 1
	}
	if ev.ArrayLength < 0 {
		ev.ArrayLength = 0
	}
	if ev.Length < 0 {
		ev.Length = 0
	}

	findings := d.handleLocked(&ev)
	if len(findings) != 0 {
		d.findings = append(d.findings, findings...)
	}
	d.mu.Unlock()

	d.notify(ev, findings)
	return findings
}

func (d *Detector) initLocked() {
	if d.threads == nil {
		d.threads = make(map[ThreadID]*threadState)
	}
}

func (d *Detector) threadLocked(thread ThreadID) *threadState {
	ts := d.threads[thread]
	if ts == nil {
		ts = &threadState{
			releases:         make(map[ArrayID]releaseRecord),
			releasedSinceGet: make(map[ArrayID]bool),
		}
		d.threads[thread] = ts
	}
	return ts
}

func (d *Detector) depthLocked(thread ThreadID) int {
	if ts := d.threads[thread]; ts != nil {
		return len(ts.active)
	}
	return 0
}

func (d *Detector) handleLocked(ev *Event) []Finding {
	switch ev.Kind {
	case EventGetCritical:
		return d.handleGetLocked(ev)
	case EventReleaseCritical:
		return d.handleReleaseLocked(ev)
	case EventJNICall:
		return d.handleJNICallLocked(ev)
	case EventBlockingCall:
		return d.handleBlockingLocked(ev)
	case EventPointerRead:
		return d.handleAccessLocked(ev, false)
	case EventPointerWrite:
		return d.handleAccessLocked(ev, true)
	case EventThreadExit:
		return d.handleThreadExitLocked(ev)
	default:
		ev.CriticalDepth = d.depthLocked(ev.Thread)
		return nil
	}
}

func (d *Detector) handleGetLocked(ev *Event) []Finding {
	ts := d.threadLocked(ev.Thread)
	ts.active = append(ts.active, &activeRegion{
		array:       ev.Array,
		pointer:     ev.Address,
		length:      ev.ArrayLength,
		elementSize: ev.ElementSize,
		getSeq:      ev.Seq,
	})
	delete(ts.releasedSinceGet, ev.Array)
	ev.CriticalDepth = len(ts.active)
	return nil
}

func (d *Detector) handleReleaseLocked(ev *Event) []Finding {
	ts := d.threadLocked(ev.Thread)

	index := -1
	for i := len(ts.active) - 1; i >= 0; i-- {
		region := ts.active[i]
		if region.array != ev.Array {
			continue
		}
		if ev.Address != 0 && region.pointer != ev.Address {
			continue
		}
		index = i
		break
	}

	if index < 0 {
		return d.releaseWithoutActiveLocked(ev, ts)
	}

	region := ts.active[index]
	ev.ArrayLength = region.length
	ev.ElementSize = region.elementSize
	if ev.Address == 0 {
		ev.Address = region.pointer
	}

	if ev.Mode == ReleaseCommit {
		region.uncommittedWrites = 0
		region.uncommittedBytes = 0
		ev.CriticalDepth = len(ts.active)
		return nil
	}

	ts.active = append(ts.active[:index], ts.active[index+1:]...)
	record := releaseRecord{
		array:       region.array,
		pointer:     region.pointer,
		length:      region.length,
		elementSize: region.elementSize,
		getSeq:      region.getSeq,
		releaseSeq:  ev.Seq,
		mode:        ev.Mode,
	}
	ts.releases[region.array] = record
	ts.releasedSinceGet[region.array] = true
	ts.releasedSpans = append(ts.releasedSpans, record)
	if len(ts.releasedSpans) > maxReleasedSpansPerThread {
		ts.releasedSpans = ts.releasedSpans[len(ts.releasedSpans)-maxReleasedSpansPerThread:]
	}

	ev.CriticalDepth = len(ts.active)

	if ev.Mode == ReleaseAbort && region.uncommittedWrites > 0 {
		finding := findingFor(ev, FindingAbortedWritesDiscarded, SeverityWarning, "")
		finding.Pointer = region.pointer
		finding.GetSeq = region.getSeq
		finding.ReleaseSeq = ev.Seq
		finding.WriteCount = region.uncommittedWrites
		finding.BytesWritten = region.uncommittedBytes
		finding.Message = fmt.Sprintf(
			"array %q released with JNI_ABORT: %d write(s) (%d byte(s)) to the critical buffer were discarded",
			ev.Array, region.uncommittedWrites, region.uncommittedBytes)
		return []Finding{finding}
	}

	return nil
}

func (d *Detector) releaseWithoutActiveLocked(ev *Event, ts *threadState) []Finding {
	previous, hasPrevious := ts.releases[ev.Array]
	if hasPrevious {
		ev.ArrayLength = previous.length
		ev.ElementSize = previous.elementSize
		if ev.Address == 0 {
			ev.Address = previous.pointer
		}
	}

	ev.CriticalDepth = len(ts.active)
	if ts.releasedSinceGet[ev.Array] {
		finding := findingFor(ev, FindingDuplicateRelease, SeverityViolation, "")
		finding.Pointer = previous.pointer
		finding.GetSeq = previous.getSeq
		finding.ReleaseSeq = previous.releaseSeq
		finding.Message = fmt.Sprintf(
			"array %q was released more than once on thread %q (first release event %d)",
			ev.Array, ev.Thread, previous.releaseSeq)
		return []Finding{finding}
	}

	finding := findingFor(ev, FindingReleaseWithoutGet, SeverityViolation, "")
	if hasPrevious {
		finding.Pointer = previous.pointer
		finding.GetSeq = previous.getSeq
		finding.ReleaseSeq = previous.releaseSeq
	}
	finding.Message = fmt.Sprintf(
		"array %q was released on thread %q without a matching Get*Critical",
		ev.Array, ev.Thread)
	return []Finding{finding}
}

func (d *Detector) handleJNICallLocked(ev *Event) []Finding {
	ts := d.threads[ev.Thread]
	if ts == nil || len(ts.active) == 0 {
		ev.CriticalDepth = 0
		return nil
	}
	ev.CriticalDepth = len(ts.active)
	if isAllowedCriticalJNICall(ev.Function) {
		return nil
	}

	region := ts.active[len(ts.active)-1]
	name := displayFunction(ev.Function)
	finding := findingFor(ev, FindingJNICallInCritical, SeverityViolation, "")
	finding.Array = region.array
	finding.ArrayLength = region.length
	finding.ElementSize = region.elementSize
	finding.Pointer = region.pointer
	finding.GetSeq = region.getSeq
	finding.Message = fmt.Sprintf(
		"JNI function %s called inside a critical section on thread %q (array %q); only Release*Critical and nested Get*Critical are allowed",
		name, ev.Thread, region.array)
	return []Finding{finding}
}

func (d *Detector) handleBlockingLocked(ev *Event) []Finding {
	ts := d.threads[ev.Thread]
	if ts == nil || len(ts.active) == 0 {
		ev.CriticalDepth = 0
		return nil
	}
	ev.CriticalDepth = len(ts.active)

	region := ts.active[len(ts.active)-1]
	name := displayFunction(ev.Function)
	finding := findingFor(ev, FindingBlockingCallInCritical, SeverityViolation, "")
	finding.Array = region.array
	finding.ArrayLength = region.length
	finding.ElementSize = region.elementSize
	finding.Pointer = region.pointer
	finding.GetSeq = region.getSeq
	finding.Message = fmt.Sprintf(
		"blocking call %s inside a critical section on thread %q (array %q); the VM may be unable to complete GC",
		name, ev.Thread, region.array)
	return []Finding{finding}
}

func (d *Detector) handleAccessLocked(ev *Event, write bool) []Finding {
	ts := d.threads[ev.Thread]
	if ts == nil {
		ev.CriticalDepth = 0
		return nil
	}
	ev.CriticalDepth = len(ts.active)

	if region := matchActiveRegion(ts, ev); region != nil {
		if ev.Array == "" {
			if record, ok := matchReleasedRegion(ts, ev); ok && !addressWithinRegion(ev.Address, region) {
				return []Finding{useAfterReleaseFinding(ev, record, write)}
			}
		}
		if write && ev.Length > 0 {
			region.uncommittedWrites++
			region.uncommittedBytes += int64(ev.Length)
		}
		if region.pointer == 0 || ev.Length == 0 {
			return nil
		}
		if !accessFits(ev.Address, ev.Length, region.pointer, region.length, region.elementSize) {
			return []Finding{d.outOfBoundsFinding(ev, region, write)}
		}
		return nil
	}

	record, ok := matchReleasedRegion(ts, ev)
	if !ok {
		return nil
	}

	return []Finding{useAfterReleaseFinding(ev, record, write)}
}

func useAfterReleaseFinding(ev *Event, record releaseRecord, write bool) Finding {
	finding := findingFor(ev, FindingUseAfterRelease, SeverityViolation, "")
	finding.Array = record.array
	finding.ArrayLength = record.length
	finding.ElementSize = record.elementSize
	finding.Pointer = record.pointer
	finding.Offset = addressOffset(ev.Address, record.pointer)
	finding.GetSeq = record.getSeq
	finding.ReleaseSeq = record.releaseSeq
	finding.Mode = record.mode
	operation := "read"
	if write {
		operation = "write"
	}
	finding.Message = fmt.Sprintf(
		"%s of %d byte(s) at 0x%x after array %q was released on thread %q (Release event %d)",
		operation, ev.Length, ev.Address, record.array, ev.Thread, record.releaseSeq)
	return finding
}

func (d *Detector) outOfBoundsFinding(ev *Event, region *activeRegion, write bool) Finding {
	kind := FindingOutOfBoundsRead
	severity := SeverityViolation
	operation := "read"
	if write {
		kind = FindingOutOfBoundsWrite
		operation = "write"
	}
	finding := findingFor(ev, kind, severity, "")
	finding.Array = region.array
	finding.ArrayLength = region.length
	finding.ElementSize = region.elementSize
	finding.Pointer = region.pointer
	finding.Offset = addressOffset(ev.Address, region.pointer)
	finding.GetSeq = region.getSeq
	allowed := byteSpan(region.length, region.elementSize)
	finding.Message = fmt.Sprintf(
		"%s of %d byte(s) at 0x%x (offset %d) is outside array %q: %d element(s) of %d byte(s), %d byte(s) total",
		operation, ev.Length, ev.Address, finding.Offset, region.array,
		region.length, region.elementSize, allowed)
	return finding
}

func (d *Detector) handleThreadExitLocked(ev *Event) []Finding {
	ts := d.threads[ev.Thread]
	if ts == nil {
		ev.CriticalDepth = 0
		return nil
	}

	findings := make([]Finding, 0, len(ts.active))
	for _, region := range ts.active {
		finding := findingFor(ev, FindingUnreleasedAtThreadExit, SeverityViolation, "")
		finding.Array = region.array
		finding.ArrayLength = region.length
		finding.ElementSize = region.elementSize
		finding.Pointer = region.pointer
		finding.GetSeq = region.getSeq
		finding.Message = fmt.Sprintf(
			"thread %q exited with array %q still pinned at 0x%x (%d element(s), %d byte(s)); Get event %d has no matching Release",
			ev.Thread, region.array, region.pointer, region.length,
			byteSpan(region.length, region.elementSize), region.getSeq)
		findings = append(findings, finding)
	}
	delete(d.threads, ev.Thread)
	ev.CriticalDepth = 0
	return findings
}

func findingFor(ev *Event, kind FindingKind, severity Severity, message string) Finding {
	return Finding{
		Kind:        kind,
		Severity:    severity,
		Thread:      ev.Thread,
		Array:       ev.Array,
		Function:    ev.Function,
		Address:     ev.Address,
		Length:      ev.Length,
		ArrayLength: ev.ArrayLength,
		ElementSize: ev.ElementSize,
		Mode:        ev.Mode,
		Message:     message,
		EventSeq:    ev.Seq,
		At:          ev.Time,
	}
}

func isAllowedCriticalJNICall(function string) bool {
	return (strings.HasPrefix(function, "Get") || strings.HasPrefix(function, "Release")) &&
		strings.HasSuffix(function, "Critical")
}

func displayFunction(function string) string {
	if function == "" {
		return "<unknown>"
	}
	return function
}

func matchActiveRegion(ts *threadState, ev *Event) *activeRegion {
	if len(ts.active) == 0 {
		return nil
	}
	if ev.Array != "" {
		for i := len(ts.active) - 1; i >= 0; i-- {
			if ts.active[i].array == ev.Array {
				return ts.active[i]
			}
		}
		return nil
	}
	if len(ts.active) == 1 {
		return ts.active[0]
	}

	address := uint64(ev.Address)
	for i := len(ts.active) - 1; i >= 0; i-- {
		region := ts.active[i]
		if region.pointer == 0 {
			continue
		}
		base := uint64(region.pointer)
		end, overflow := addOverflow(base, byteSpan(region.length, region.elementSize))
		if overflow {
			if address >= base {
				return region
			}
			continue
		}
		if address >= base && address <= end {
			return region
		}
	}
	return nil
}

func matchReleasedRegion(ts *threadState, ev *Event) (releaseRecord, bool) {
	if ev.Array != "" {
		if record, ok := ts.releases[ev.Array]; ok {
			return record, true
		}
	}

	address := uint64(ev.Address)
	for i := len(ts.releasedSpans) - 1; i >= 0; i-- {
		record := ts.releasedSpans[i]
		if record.pointer == 0 {
			continue
		}
		base := uint64(record.pointer)
		end, overflow := addOverflow(base, byteSpan(record.length, record.elementSize))
		if overflow {
			if address >= base {
				return record, true
			}
			continue
		}
		if address >= base && address < end {
			return record, true
		}
		if ev.Length == 0 && address == end {
			return record, true
		}
	}
	return releaseRecord{}, false
}

func addressWithinRegion(address uintptr, region *activeRegion) bool {
	if region.pointer == 0 {
		return false
	}
	start := uint64(address)
	base := uint64(region.pointer)
	end, overflow := addOverflow(base, byteSpan(region.length, region.elementSize))
	if overflow {
		return start >= base
	}
	if end == base {
		return start == base
	}
	return start >= base && start < end
}

func accessFits(address uintptr, length int, base uintptr, arrayLength, elementSize int) bool {
	start := uint64(address)
	size := uint64(length)
	end, accessOverflow := addOverflow(start, size)
	if accessOverflow {
		return false
	}

	baseAddress := uint64(base)
	span := byteSpan(arrayLength, elementSize)
	allowedEnd, spanOverflow := addOverflow(baseAddress, span)
	if spanOverflow {
		return false
	}
	return start >= baseAddress && end <= allowedEnd
}

func addressOffset(address, base uintptr) int64 {
	if address >= base {
		delta := uint64(address - base)
		if delta > uint64(maxInt64) {
			return maxInt64
		}
		return int64(delta)
	}
	delta := uint64(base - address)
	if delta > uint64(maxInt64) {
		return -maxInt64
	}
	return -int64(delta)
}

func addOverflow(left, right uint64) (uint64, bool) {
	sum := left + right
	return sum, sum < left
}

const maxInt64 = int64(^uint64(0) >> 1)

// Subscribe registers a callback for every normalized event. The returned
// function unsubscribes the callback and is safe to call more than once.
func (d *Detector) Subscribe(fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}

	d.subMu.Lock()
	d.nextSubscription++
	id := d.nextSubscription
	d.eventSubscribers = append(d.eventSubscribers, eventSubscription{id: id, fn: fn})
	d.subMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.subMu.Lock()
			subscribers := d.eventSubscribers[:0]
			for _, subscriber := range d.eventSubscribers {
				if subscriber.id != id {
					subscribers = append(subscribers, subscriber)
				}
			}
			d.eventSubscribers = subscribers
			d.subMu.Unlock()
		})
	}
}

// SubscribeFindings registers a callback for every finding. The returned
// function unsubscribes the callback and is safe to call more than once.
func (d *Detector) SubscribeFindings(fn func(Finding)) func() {
	if fn == nil {
		return func() {}
	}

	d.subMu.Lock()
	d.nextSubscription++
	id := d.nextSubscription
	d.findingSubscribers = append(d.findingSubscribers, findingSubscription{id: id, fn: fn})
	d.subMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			d.subMu.Lock()
			subscribers := d.findingSubscribers[:0]
			for _, subscriber := range d.findingSubscribers {
				if subscriber.id != id {
					subscribers = append(subscribers, subscriber)
				}
			}
			d.findingSubscribers = subscribers
			d.subMu.Unlock()
		})
	}
}

func (d *Detector) notify(ev Event, findings []Finding) {
	d.subMu.RLock()
	eventCallbacks := make([]func(Event), len(d.eventSubscribers))
	for i, subscriber := range d.eventSubscribers {
		eventCallbacks[i] = subscriber.fn
	}
	findingCallbacks := make([]func(Finding), len(d.findingSubscribers))
	for i, subscriber := range d.findingSubscribers {
		findingCallbacks[i] = subscriber.fn
	}
	d.subMu.RUnlock()

	for _, callback := range eventCallbacks {
		callback(ev)
	}
	for _, finding := range findings {
		for _, callback := range findingCallbacks {
			callback(finding)
		}
	}
}

// Findings returns a copy of all findings recorded so far.
func (d *Detector) Findings() []Finding {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Finding(nil), d.findings...)
}

// Active returns the critical regions currently pinned by thread.
func (d *Detector) Active(thread ThreadID) []Region {
	d.mu.Lock()
	defer d.mu.Unlock()

	ts := d.threads[thread]
	if ts == nil {
		return nil
	}
	return regionsLocked(thread, ts)
}

// ActiveAll returns all currently pinned critical regions. Regions are ordered
// by thread and then by nesting depth.
func (d *Detector) ActiveAll() []Region {
	d.mu.Lock()
	defer d.mu.Unlock()

	var regions []Region
	for thread, ts := range d.threads {
		regions = append(regions, regionsLocked(thread, ts)...)
	}
	sort.Slice(regions, func(i, j int) bool {
		if regions[i].Thread != regions[j].Thread {
			return regions[i].Thread < regions[j].Thread
		}
		return regions[i].Depth < regions[j].Depth
	})
	return regions
}

func regionsLocked(thread ThreadID, ts *threadState) []Region {
	regions := make([]Region, 0, len(ts.active))
	for depth, region := range ts.active {
		regions = append(regions, Region{
			Thread:      thread,
			Array:       region.array,
			Pointer:     region.pointer,
			Length:      region.length,
			ElementSize: region.elementSize,
			GetSeq:      region.getSeq,
			Depth:       depth,
		})
	}
	return regions
}

// Reset clears detector state and accumulated findings.
func (d *Detector) Reset() {
	d.mu.Lock()
	d.seq = 0
	d.threads = make(map[ThreadID]*threadState)
	d.findings = nil
	d.mu.Unlock()
}

// Analyze runs a batch of events through a fresh detector and returns all
// findings. It is a convenience helper for offline traces.
func Analyze(events []Event) []Finding {
	detector := NewDetector()
	for _, event := range events {
		detector.Observe(event)
	}
	return detector.Findings()
}
