package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultPageSize is the page size used by NewPool and NewHost.
const DefaultPageSize = 4096

var (
	// ErrPoolExhausted means that the pool has no scrubbed page available.
	ErrPoolExhausted = errors.New("netsec: page pool exhausted")
	// ErrInvalidPage means that a physical page ID does not belong to the pool.
	ErrInvalidPage = errors.New("netsec: invalid page")
	// ErrPageUnavailable means that a page is reserved or being scrubbed and
	// therefore must not be observed by a caller.
	ErrPageUnavailable = errors.New("netsec: page unavailable")
	// ErrZeroizeVerification means that a Zeroizer reported success but did
	// not leave the page entirely zeroed.
	ErrZeroizeVerification = errors.New("netsec: zeroization verification failed")
	// ErrZeroizePanic means that the Zeroizer panicked.
	ErrZeroizePanic = errors.New("netsec: zeroizer panicked")
)

// PageID identifies a physical page owned by a Pool. IDs are stable only for
// the lifetime of the Pool.
type PageID uint64

// PageState is the lifecycle state of a physical page.
type PageState uint8

const (
	// PageInUse means the page has at least one authorized snapshot reference.
	PageInUse PageState = iota
	// PageZeroing means the last reference was released and scrubbing is in
	// progress. The page is not allocatable.
	PageZeroing
	// PageFree means the page has been scrubbed and may be allocated again.
	PageFree
	// PageQuarantined means scrubbing failed. The page is never allocated
	// again.
	PageQuarantined
)

// String returns a stable lowercase name for s.
func (s PageState) String() string {
	switch s {
	case PageInUse:
		return "in-use"
	case PageZeroing:
		return "zeroing"
	case PageFree:
		return "free"
	case PageQuarantined:
		return "quarantined"
	default:
		return "unknown"
	}
}

// Stats is a snapshot of physical-page and reference accounting.
type Stats struct {
	PageSize    int
	Capacity    int
	InUse       int
	Zeroing     int
	Free        int
	Quarantined int
	// References is the total number of authorized snapshot-to-page
	// references. A shared physical page contributes its reference count.
	References int
}

// PageInfo describes one physical page. ZeroError is non-nil only for a
// quarantined page.
type PageInfo struct {
	ID           PageID
	State        PageState
	Refs         int
	ZeroAttempts int
	ZeroError    error
}

// ZeroError identifies a page that could not be scrubbed.
type ZeroError struct {
	Page    PageID
	Attempt int
	Err     error
}

func (e *ZeroError) Error() string {
	return fmt.Sprintf("netsec: page %d zeroization attempt %d failed: %v", e.Page, e.Attempt, e.Err)
}

// Unwrap exposes the Zeroizer or verification error.
func (e *ZeroError) Unwrap() error {
	return e.Err
}

type pageRecord struct {
	id           PageID
	data         []byte
	state        PageState
	refs         int
	zeroAttempts int
	zeroErr      error
}

// Pool owns a fixed set of physical pages. Pages are shared by snapshots
// through counted references and are only returned to the free list after
// their last reference has been scrubbed.
type Pool struct {
	mu       sync.Mutex
	zeroMu   sync.Mutex
	pageSize int
	zeroer   Zeroizer
	pages    []*pageRecord
	free     []PageID
	refs     int
}

// Host is the memory-host facing name for a Pool.
type Host = Pool

// NewPool creates a pool of pageCount pages using DefaultPageSize. A nil
// zeroer selects ZeroBytes.
func NewPool(pageCount int, zeroer Zeroizer) (*Pool, error) {
	return NewPoolWithPageSize(DefaultPageSize, pageCount, zeroer)
}

// NewPoolWithPageSize creates a pool with an explicit page size. The page size
// is configurable so hosts and tests can use very small pages.
func NewPoolWithPageSize(pageSize, pageCount int, zeroer Zeroizer) (*Pool, error) {
	if pageSize <= 0 {
		return nil, fmt.Errorf("netsec: page size must be positive: %d", pageSize)
	}
	if pageCount < 0 {
		return nil, fmt.Errorf("netsec: page count must not be negative: %d", pageCount)
	}
	if zeroer == nil {
		zeroer = ZeroizerFunc(ZeroBytes)
	}

	p := &Pool{
		pageSize: pageSize,
		zeroer:   zeroer,
		pages:    make([]*pageRecord, 0, pageCount),
		free:     make([]PageID, 0, pageCount),
	}
	for i := 0; i < pageCount; i++ {
		id := PageID(i)
		p.pages = append(p.pages, &pageRecord{
			id:    id,
			data:  make([]byte, pageSize),
			state: PageFree,
		})
		p.free = append(p.free, id)
	}
	return p, nil
}

// NewHost is the memory-host constructor counterpart to NewPool.
func NewHost(pageCount int, zeroer Zeroizer) (*Host, error) {
	return NewPool(pageCount, zeroer)
}

// NewHostWithPageSize is the memory-host constructor counterpart to
// NewPoolWithPageSize.
func NewHostWithPageSize(pageSize, pageCount int, zeroer Zeroizer) (*Host, error) {
	return NewPoolWithPageSize(pageSize, pageCount, zeroer)
}

// PageSize returns the pool's page size.
func (p *Pool) PageSize() int {
	return p.pageSize
}

func (p *Pool) record(id PageID) (*pageRecord, error) {
	if uint64(id) >= uint64(len(p.pages)) {
		return nil, fmt.Errorf("%w: %d", ErrInvalidPage, id)
	}
	return p.pages[int(id)], nil
}

func (p *Pool) acquireLocked(count int) ([]PageID, error) {
	if count > len(p.free) {
		return nil, fmt.Errorf("%w: need %d, have %d", ErrPoolExhausted, count, len(p.free))
	}
	ids := make([]PageID, count)
	for i := range ids {
		last := len(p.free) - 1
		id := p.free[last]
		p.free = p.free[:last]
		rec := p.pages[int(id)]
		if rec.state != PageFree {
			panic("netsec: free list contains a non-free page")
		}
		// Free pages are already zero, but scrub again before exposing one to
		// a new owner. This also protects against a faulty prior Zeroizer that
		// reported success and was not otherwise detected.
		clear(rec.data)
		rec.state = PageInUse
		rec.refs = 0
		rec.zeroErr = nil
		ids[i] = id
	}
	return ids, nil
}

func (p *Pool) newSnapshot(data []byte) ([]PageID, error) {
	pageCount := (len(data) + p.pageSize - 1) / p.pageSize

	p.mu.Lock()
	defer p.mu.Unlock()

	ids, err := p.acquireLocked(pageCount)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		rec := p.pages[int(id)]
		start := i * p.pageSize
		end := start + p.pageSize
		if end > len(data) {
			end = len(data)
		}
		copy(rec.data, data[start:end])
		rec.refs = 1
	}
	p.refs += pageCount
	return ids, nil
}

func (p *Pool) retain(ids []PageID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, id := range ids {
		rec, err := p.record(id)
		if err != nil {
			return err
		}
		if rec.state != PageInUse || rec.refs < 1 {
			return fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
		}
	}
	for _, id := range ids {
		p.pages[int(id)].refs++
	}
	p.refs += len(ids)
	return nil
}

// copyForWrite reserves new pages for every shared page in ids and returns an
// old-to-new mapping. It either reserves all needed pages or changes nothing.
func (p *Pool) copyForWrite(ids []PageID) (map[PageID]PageID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	shared := 0
	for _, id := range ids {
		rec, err := p.record(id)
		if err != nil {
			return nil, err
		}
		if rec.state != PageInUse || rec.refs < 1 {
			return nil, fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
		}
		if rec.refs > 1 {
			shared++
		}
	}
	if shared == 0 {
		return nil, nil
	}

	newIDs, err := p.acquireLocked(shared)
	if err != nil {
		return nil, err
	}

	replacements := make(map[PageID]PageID, shared)
	next := 0
	for _, oldID := range ids {
		old := p.pages[int(oldID)]
		if old.refs <= 1 {
			continue
		}
		newID := newIDs[next]
		next++
		fresh := p.pages[int(newID)]
		copy(fresh.data, old.data)
		fresh.refs = 1
		old.refs--
		// The old reference is replaced by the new one, so p.refs is
		// unchanged.
		replacements[oldID] = newID
	}
	return replacements, nil
}

func (p *Pool) readPage(id PageID, offset int, dst []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, err := p.record(id)
	if err != nil {
		return err
	}
	if rec.state != PageInUse || rec.refs < 1 {
		return fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
	}
	if offset < 0 || offset+len(dst) > len(rec.data) {
		return fmt.Errorf("netsec: page %d range [%d,%d) is invalid", id, offset, offset+len(dst))
	}
	copy(dst, rec.data[offset:offset+len(dst)])
	return nil
}

func (p *Pool) writePage(id PageID, offset int, src []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, err := p.record(id)
	if err != nil {
		return err
	}
	if rec.state != PageInUse || rec.refs < 1 {
		return fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
	}
	if offset < 0 || offset+len(src) > len(rec.data) {
		return fmt.Errorf("netsec: page %d range [%d,%d) is invalid", id, offset, offset+len(src))
	}
	copy(rec.data[offset:], src)
	return nil
}

func (p *Pool) release(ids []PageID) error {
	var toZero []PageID

	p.mu.Lock()
	// Validate the whole release before changing any accounting so an
	// internal mapping error cannot leave a snapshot half-released.
	for _, id := range ids {
		rec, err := p.record(id)
		if err != nil {
			p.mu.Unlock()
			return err
		}
		if rec.state != PageInUse || rec.refs < 1 {
			p.mu.Unlock()
			return fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
		}
	}
	for _, id := range ids {
		rec := p.pages[int(id)]
		rec.refs--
		p.refs--
		if rec.refs == 0 {
			// Mark the page unavailable before dropping the lock. A paused
			// Zeroizer therefore prevents both reuse and observation.
			rec.state = PageZeroing
			toZero = append(toZero, id)
		}
	}
	p.mu.Unlock()

	var errs []error
	for _, id := range toZero {
		if err := p.zeroPage(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Pool) zeroPage(id PageID) error {
	p.mu.Lock()
	rec, err := p.record(id)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	if rec.state != PageZeroing {
		p.mu.Unlock()
		return fmt.Errorf("netsec: page %d is not awaiting zeroization", id)
	}
	rec.zeroAttempts++
	attempt := rec.zeroAttempts
	page := rec.data
	p.mu.Unlock()

	// Serialize scrubber calls. A host-provided Zeroizer need not be safe for
	// concurrent use, and a paused scrub must not block unrelated pool
	// accounting or allocation.
	p.zeroMu.Lock()
	err = scrub(p.zeroer, page)
	if err == nil && !allZero(page) {
		err = ErrZeroizeVerification
	}
	p.zeroMu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	rec = p.pages[int(id)]
	if err != nil {
		wrapped := &ZeroError{Page: id, Attempt: attempt, Err: err}
		rec.state = PageQuarantined
		rec.zeroErr = wrapped
		return wrapped
	}
	rec.state = PageFree
	rec.zeroErr = nil
	p.free = append(p.free, id)
	return nil
}

// Stats returns current per-state counts and total authorized references.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := Stats{
		PageSize: p.pageSize,
		Capacity: len(p.pages),
	}
	for _, rec := range p.pages {
		switch rec.state {
		case PageInUse:
			stats.InUse++
		case PageZeroing:
			stats.Zeroing++
		case PageFree:
			stats.Free++
		case PageQuarantined:
			stats.Quarantined++
		}
		stats.References += rec.refs
	}
	return stats
}

// PageInfo returns accounting information for a physical page.
func (p *Pool) PageInfo(id PageID) (PageInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, err := p.record(id)
	if err != nil {
		return PageInfo{}, err
	}
	return pageInfo(rec), nil
}

// Inspect returns information for every physical page in ID order.
func (p *Pool) Inspect() []PageInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]PageInfo, len(p.pages))
	for i, rec := range p.pages {
		result[i] = pageInfo(rec)
	}
	return result
}

func pageInfo(rec *pageRecord) PageInfo {
	return PageInfo{
		ID:           rec.id,
		State:        rec.state,
		Refs:         rec.refs,
		ZeroAttempts: rec.zeroAttempts,
		ZeroError:    rec.zeroErr,
	}
}

// PageBytes returns a copy of a physical page's bytes. It refuses to expose a
// reserved or currently scrubbing page.
func (p *Pool) PageBytes(id PageID) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, err := p.record(id)
	if err != nil {
		return nil, err
	}
	if rec.state == PageZeroing || (rec.state == PageInUse && rec.refs == 0) {
		return nil, fmt.Errorf("%w: page %d is %s", ErrPageUnavailable, id, rec.state)
	}
	return append([]byte(nil), rec.data...), nil
}

// Check validates page states, free-list membership, reference totals and the
// all-zero invariant for free pages. It is intended for host tests and
// diagnostics.
func (p *Pool) Check() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	seenFree := make(map[PageID]bool, len(p.free))
	freeCount := 0
	refs := 0

	for i, rec := range p.pages {
		id := PageID(i)
		if rec.id != id {
			errs = append(errs, fmt.Errorf("page slot %d has id %d", i, rec.id))
		}
		if rec.refs < 0 {
			errs = append(errs, fmt.Errorf("page %d has negative refs %d", id, rec.refs))
		}
		refs += rec.refs
		switch rec.state {
		case PageInUse:
			if rec.refs < 1 {
				errs = append(errs, fmt.Errorf("in-use page %d has no references", id))
			}
		case PageZeroing:
			if rec.refs != 0 {
				errs = append(errs, fmt.Errorf("zeroing page %d has refs %d", id, rec.refs))
			}
		case PageFree:
			freeCount++
			seenFree[id] = true
			if rec.refs != 0 {
				errs = append(errs, fmt.Errorf("free page %d has refs %d", id, rec.refs))
			}
			if !allZero(rec.data) {
				errs = append(errs, fmt.Errorf("free page %d is not zeroed", id))
			}
		case PageQuarantined:
			if rec.refs != 0 {
				errs = append(errs, fmt.Errorf("quarantined page %d has refs %d", id, rec.refs))
			}
			if rec.zeroErr == nil {
				errs = append(errs, fmt.Errorf("quarantined page %d has no error", id))
			}
		default:
			errs = append(errs, fmt.Errorf("page %d has unknown state %d", id, rec.state))
		}
	}
	if freeCount != len(p.free) {
		errs = append(errs, fmt.Errorf("free list has %d entries for %d free pages", len(p.free), freeCount))
	}
	if len(seenFree) != len(p.free) {
		errs = append(errs, errors.New("free list contains duplicate or non-free pages"))
	}
	for _, id := range p.free {
		if !seenFree[id] {
			errs = append(errs, fmt.Errorf("free list contains non-free page %d", id))
		}
	}
	if refs != p.refs {
		errs = append(errs, fmt.Errorf("reference total is %d, pages account for %d", p.refs, refs))
	}
	return errors.Join(errs...)
}
