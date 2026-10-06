// Package cachemodel implements a cache page whose dirty state is tracked as
// byte ranges, plus a per-byte version reference model used to verify it.
//
// Writeback captures the page version and the dirty set at begin time. Writes
// that land while a writeback is in flight bump the page version, so when the
// old writeback completes it only clears the bytes it actually wrote and only
// if they are still at the captured version; bytes re-dirtied in the meantime
// stay dirty.
package cachemodel

import "sync"

// Interval is a half-open byte range [Start, End).
type Interval struct {
	Start, End int
}

// Op identifies the kind of linearization event emitted by a Page.
type Op int

const (
	OpWrite Op = iota
	OpWritebackBegin
	OpWritebackComplete
	OpEvict
	OpDiskWrite // emitted by the writeback worker, not by Page
)

// Event is emitted at each linearization point of the cache so tests can
// replay the exact same sequence against the per-byte version Model.
type Event struct {
	Op      Op
	Version uint64 // page version at the time of the event
	Token   uint64 // writeback token, for begin/complete
	Off     int    // OpWrite / OpDiskWrite offset
	Data    []byte // OpWrite / OpDiskWrite payload
	Written []Interval
}

// dirtyInterval is a dirty byte range tagged with the page version at which
// it was last dirtied.
type dirtyInterval struct {
	Interval
	version uint64
}

// Writeback is a snapshot of the dirty state to be persisted.
type Writeback struct {
	token   uint64
	version uint64 // page version captured at begin
	// Dirty is the dirty set captured at begin; Data is a snapshot of the
	// whole page at the same instant.
	Dirty []Interval
	Data  []byte
}

// Page is a single cache page with byte-range dirty tracking.
type Page struct {
	mu         sync.Mutex
	cond       *sync.Cond
	data       []byte
	dirty      []dirtyInterval // sorted by Start, non-overlapping
	version    uint64          // bumped on every Write
	wbInFlight bool
	wbToken    uint64
	observer   func(Event) // if set, called with p.mu held
}

func NewPage(size int) *Page {
	p := &Page{data: make([]byte, size)}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *Page) Size() int { return len(p.data) }

// Write copies b into the page at off and marks the range dirty at a fresh
// page version.
func (p *Page) Write(off int, b []byte) {
	if len(b) == 0 || off < 0 || off+len(b) > len(p.data) {
		panic("cachemodel: write out of range")
	}
	p.mu.Lock()
	p.version++
	copy(p.data[off:], b)
	p.dirty = spliceInterval(p.dirty, dirtyInterval{Interval{off, off + len(b)}, p.version})
	if p.observer != nil {
		p.observer(Event{Op: OpWrite, Version: p.version, Off: off, Data: append([]byte(nil), b...)})
	}
	p.mu.Unlock()
}

// BeginWriteback captures the current page version and dirty set. At most one
// writeback may be in flight; it returns ok=false if one is already running
// or the page is clean.
func (p *Page) BeginWriteback() (*Writeback, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wbInFlight || len(p.dirty) == 0 {
		return nil, false
	}
	p.wbInFlight = true
	p.wbToken++
	dirty := make([]Interval, len(p.dirty))
	for i, iv := range p.dirty {
		dirty[i] = iv.Interval
	}
	wb := &Writeback{
		token:   p.wbToken,
		version: p.version,
		Dirty:   dirty,
		Data:    append([]byte(nil), p.data...),
	}
	if p.observer != nil {
		p.observer(Event{Op: OpWritebackBegin, Version: p.version, Token: wb.token})
	}
	return wb, true
}

// CompleteWriteback finishes the in-flight writeback. written lists the byte
// ranges that were actually persisted (a subset of wb.Dirty, possibly
// smaller after a short write). Only bytes still at or below the captured
// version are cleared; bytes re-dirtied since begin stay dirty.
func (p *Page) CompleteWriteback(wb *Writeback, written []Interval) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.wbInFlight || wb.token != p.wbToken {
		panic("cachemodel: stale or unknown writeback")
	}
	for _, w := range written {
		if !coveredBy(wb.Dirty, w) {
			panic("cachemodel: completed range was not dirty at capture")
		}
		p.dirty = subtractWritten(p.dirty, w, wb.version)
	}
	p.wbInFlight = false
	if p.observer != nil {
		cp := append([]Interval(nil), written...)
		p.observer(Event{Op: OpWritebackComplete, Version: p.version, Token: wb.token, Written: cp})
	}
	p.cond.Broadcast()
}

// Evict blocks until the page is fully clean and no writeback is in flight,
// then returns the page contents.
func (p *Page) Evict() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.dirty) > 0 || p.wbInFlight {
		p.cond.Wait()
	}
	if p.observer != nil {
		p.observer(Event{Op: OpEvict, Version: p.version})
	}
	return append([]byte(nil), p.data...)
}

// FlushAll writes every dirty range to d and waits until the page is clean.
func (p *Page) FlushAll(d *Disk) {
	for {
		p.mu.Lock()
		for p.wbInFlight {
			p.cond.Wait()
		}
		clean := len(p.dirty) == 0
		p.mu.Unlock()
		if clean {
			return
		}
		wb, ok := p.BeginWriteback()
		if !ok {
			continue
		}
		for _, iv := range wb.Dirty {
			d.Write(iv.Start, wb.Data[iv.Start:iv.End])
		}
		p.CompleteWriteback(wb, wb.Dirty)
	}
}

// IsClean reports whether the page has no dirty bytes and no writeback in
// flight.
func (p *Page) IsClean() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.dirty) == 0 && !p.wbInFlight
}

// DirtyIntervals returns the current dirty set.
func (p *Page) DirtyIntervals() []Interval {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Interval, len(p.dirty))
	for i, iv := range p.dirty {
		out[i] = iv.Interval
	}
	return out
}

// Data returns a copy of the page contents.
func (p *Page) Data() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.data...)
}

// spliceInterval inserts n into the sorted set, replacing any covered ranges
// and merging adjacent ranges that share n's version.
func spliceInterval(set []dirtyInterval, n dirtyInterval) []dirtyInterval {
	out := make([]dirtyInterval, 0, len(set)+1)
	for _, iv := range set {
		if iv.End <= n.Start || iv.Start >= n.End {
			out = append(out, iv)
			continue
		}
		if iv.Start < n.Start {
			out = append(out, dirtyInterval{Interval{iv.Start, n.Start}, iv.version})
		}
		if iv.End > n.End {
			out = append(out, dirtyInterval{Interval{n.End, iv.End}, iv.version})
		}
	}
	pos := 0
	for pos < len(out) && out[pos].Start < n.Start {
		pos++
	}
	out = append(out, dirtyInterval{})
	copy(out[pos+1:], out[pos:])
	out[pos] = n
	// Merge neighbours with the same version.
	merged := out[:0]
	for _, iv := range out {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if last.End == iv.Start && last.version == iv.version {
				last.End = iv.End
				continue
			}
		}
		merged = append(merged, iv)
	}
	return merged
}

// subtractWritten removes w from every interval whose version is at or below
// maxVersion. Intervals dirtied at a newer version are left untouched.
func subtractWritten(set []dirtyInterval, w Interval, maxVersion uint64) []dirtyInterval {
	out := make([]dirtyInterval, 0, len(set))
	for _, iv := range set {
		if iv.version > maxVersion || iv.End <= w.Start || iv.Start >= w.End {
			out = append(out, iv)
			continue
		}
		if iv.Start < w.Start {
			out = append(out, dirtyInterval{Interval{iv.Start, w.Start}, iv.version})
		}
		if iv.End > w.End {
			out = append(out, dirtyInterval{Interval{w.End, iv.End}, iv.version})
		}
	}
	return out
}

// coveredBy reports whether w is fully contained in the union of set.
func coveredBy(set []Interval, w Interval) bool {
	if w.Start >= w.End {
		return false
	}
	pos := w.Start
	for _, iv := range set {
		if iv.Start > pos {
			return false
		}
		if iv.End > pos {
			pos = iv.End
		}
		if pos >= w.End {
			return true
		}
	}
	return false
}

// Disk is a trivial block device used by tests and FlushAll.
type Disk struct {
	mu   sync.Mutex
	data []byte
}

func NewDisk(size int) *Disk { return &Disk{data: make([]byte, size)} }

func (d *Disk) Write(off int, b []byte) {
	d.mu.Lock()
	copy(d.data[off:], b)
	d.mu.Unlock()
}

func (d *Disk) Bytes() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.data...)
}
