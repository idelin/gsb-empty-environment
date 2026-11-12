package taskpkg

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrWaitingLimit is hit when one more waiting section would exceed
	// Limits.MaxWaitingSections.
	ErrWaitingLimit = errors.New("taskpkg: waiting section count limit exceeded")
	// ErrWaitingBytesLimit is hit when one more waiting section would
	// exceed Limits.MaxWaitingBytes of buffered encoded data.
	ErrWaitingBytesLimit = errors.New("taskpkg: waiting encoded bytes limit exceeded")
	// ErrWaitTimeout is hit when a section stays blocked past Limits.MaxWait.
	ErrWaitTimeout = errors.New("taskpkg: section wait deadline exceeded")
	// ErrIllegalReference is hit when a section references an entry that
	// was never inserted, was already evicted, or belongs to another
	// connection generation.
	ErrIllegalReference = errors.New("taskpkg: illegal dynamic table reference")
	// ErrDuplicateSection is hit when a section id is admitted twice.
	ErrDuplicateSection = errors.New("taskpkg: duplicate section id")
	// ErrSectionBlocked is returned when decoding a section that is still
	// waiting for table insertions. It is not a connection error.
	ErrSectionBlocked = errors.New("taskpkg: section is still blocked")
	// ErrUnknownSection is returned when decoding or completing a section
	// that was never admitted or was already cancelled.
	ErrUnknownSection = errors.New("taskpkg: unknown section id")
)

// Limits cap the resources a peer may consume with blocked sections.
// Each limit is enforced independently.
type Limits struct {
	MaxWaitingSections int           // max simultaneously blocked sections
	MaxWaitingBytes    int           // max cumulative encoded bytes of blocked sections
	MaxWait            time.Duration // max time a section may stay blocked
}

// Section is an encoded header block as declared by the peer: the
// required insert count, the absolute entry numbers it references, and
// the connection generation it belongs to.
type Section struct {
	ID                  uint64
	Gen                 uint64
	RequiredInsertCount uint64
	Refs                []uint64
	EncodedLen          int
}

// Outcome is the result of admitting a section.
type Outcome int

const (
	// Ready means the section may be decoded immediately.
	Ready Outcome = iota
	// Waiting means the section is blocked until the table reaches the
	// declared required insert count.
	Waiting
)

type tracked struct {
	sec      Section
	waiting  bool
	deadline time.Time
	holds    []uint64 // pins owned by this section alone
}

// Conn is the header admission controller for one protocol connection.
// Any protocol violation terminates the connection with zero delivery.
type Conn struct {
	table  *DynamicTable
	limits Limits
	now    func() time.Time

	sections  map[uint64]*tracked
	waiters   int
	waitBytes int

	terminated bool
	termErr    error
	delivered  []uint64
}

// NewConn returns a Conn bound to table. now may be nil, in which case
// the wall clock is used; tests supply a controllable clock.
func NewConn(table *DynamicTable, limits Limits, now func() time.Time) *Conn {
	if now == nil {
		now = time.Now
	}
	return &Conn{
		table:    table,
		limits:   limits,
		now:      now,
		sections: make(map[uint64]*tracked),
	}
}

// Terminated reports whether the connection has been shut down.
func (c *Conn) Terminated() bool { return c.terminated }

// Err returns the error that terminated the connection, if any.
func (c *Conn) Err() error { return c.termErr }

// Delivered returns the ids of sections decoded successfully so far.
func (c *Conn) Delivered() []uint64 { return append([]uint64(nil), c.delivered...) }

// WaitingSections returns the number of currently blocked sections.
func (c *Conn) WaitingSections() int { return c.waiters }

// WaitingBytes returns the encoded bytes currently held by blocked sections.
func (c *Conn) WaitingBytes() int { return c.waitBytes }

func (c *Conn) terminate(err error) error {
	if c.terminated {
		return c.termErr
	}
	for _, tr := range c.sections {
		c.release(tr)
	}
	c.terminated = true
	c.termErr = err
	c.sections = make(map[uint64]*tracked)
	c.waiters = 0
	c.waitBytes = 0
	return c.termErr
}

func (c *Conn) alive() error {
	if c.terminated {
		return c.termErr
	}
	c.expire()
	if c.terminated {
		return c.termErr
	}
	return nil
}

// expire terminates the connection if any blocked section has exceeded
// its wait deadline.
func (c *Conn) expire() {
	if c.terminated {
		return
	}
	now := c.now()
	for _, tr := range c.sections {
		if tr.waiting && !now.Before(tr.deadline) {
			c.terminate(fmt.Errorf("%w: section %d", ErrWaitTimeout, tr.sec.ID))
			return
		}
	}
}

// Tick advances admission housekeeping; with an injected clock it lets
// tests observe wait-deadline enforcement deterministically.
func (c *Conn) Tick() { c.expire() }

// release drops the pins owned by tr and returns its resources. It only
// ever touches tr's own holds, never another section's.
func (c *Conn) release(tr *tracked) {
	if !tr.waiting {
		return
	}
	for _, ref := range tr.holds {
		c.table.Unpin(ref)
	}
	tr.holds = nil
	tr.waiting = false
	c.waiters--
	c.waitBytes -= tr.sec.EncodedLen
}

// Admit registers a section. If the table already satisfies the declared
// required insert count the section is Ready; otherwise it is parked as
// Waiting subject to the configured limits. Any inconsistency terminates
// the connection.
func (c *Conn) Admit(sec Section) (Outcome, error) {
	if err := c.alive(); err != nil {
		return Waiting, err
	}
	if sec.EncodedLen < 0 {
		sec.EncodedLen = 0
	}
	if _, dup := c.sections[sec.ID]; dup {
		return Waiting, c.terminate(fmt.Errorf("%w: %d", ErrDuplicateSection, sec.ID))
	}
	if sec.Gen != c.table.Gen() {
		return Waiting, c.terminate(fmt.Errorf("%w: section gen %d, table gen %d",
			ErrIllegalReference, sec.Gen, c.table.Gen()))
	}
	for _, ref := range sec.Refs {
		if ref >= sec.RequiredInsertCount {
			return Waiting, c.terminate(fmt.Errorf("%w: ref %d >= required insert count %d",
				ErrIllegalReference, ref, sec.RequiredInsertCount))
		}
	}
	tr := &tracked{sec: sec}
	if c.table.Inserted() >= sec.RequiredInsertCount {
		c.sections[sec.ID] = tr
		return Ready, nil
	}
	if c.waiters+1 > c.limits.MaxWaitingSections {
		return Waiting, c.terminate(ErrWaitingLimit)
	}
	if c.waitBytes+sec.EncodedLen > c.limits.MaxWaitingBytes {
		return Waiting, c.terminate(ErrWaitingBytesLimit)
	}
	tr.waiting = true
	tr.deadline = c.now().Add(c.limits.MaxWait)
	tr.holds = append([]uint64(nil), sec.Refs...)
	for _, ref := range tr.holds {
		c.table.Pin(ref)
	}
	c.sections[sec.ID] = tr
	c.waiters++
	c.waitBytes += sec.EncodedLen
	return Waiting, nil
}

// Insert adds an entry to the connection's dynamic table and promotes
// any sections unblocked by it.
func (c *Conn) Insert(name, value string) (uint64, error) {
	if err := c.alive(); err != nil {
		return 0, err
	}
	idx, err := c.table.Insert(name, value)
	if err != nil {
		return 0, c.terminate(err)
	}
	c.promote()
	return idx, nil
}

func (c *Conn) promote() {
	inserted := c.table.Inserted()
	for _, tr := range c.sections {
		if tr.waiting && inserted >= tr.sec.RequiredInsertCount {
			c.release(tr)
		}
	}
}

// Cancel withdraws a section. It is idempotent and a no-op for unknown
// ids; cancelling one section never releases references held by another.
func (c *Conn) Cancel(id uint64) {
	if c.terminated {
		return
	}
	tr, ok := c.sections[id]
	if !ok {
		return
	}
	c.release(tr)
	delete(c.sections, id)
}

// Decode validates every referenced entry — inserted, not evicted, and
// belonging to the section's generation — before delivering anything.
// An illegal reference terminates the connection and delivers nothing.
func (c *Conn) Decode(id uint64) ([]Entry, error) {
	if err := c.alive(); err != nil {
		return nil, err
	}
	tr, ok := c.sections[id]
	if !ok {
		return nil, ErrUnknownSection
	}
	if tr.waiting {
		return nil, ErrSectionBlocked
	}
	out := make([]Entry, 0, len(tr.sec.Refs))
	for _, ref := range tr.sec.Refs {
		e, ok := c.table.Get(ref)
		if !ok || e.Gen != tr.sec.Gen {
			c.terminate(fmt.Errorf("%w: entry %d", ErrIllegalReference, ref))
			return nil, c.termErr
		}
		out = append(out, e)
	}
	delete(c.sections, id)
	c.delivered = append(c.delivered, id)
	return out, nil
}

// Reissue moves the connection to a new generation: all parked sections
// are released and the table is reset. Sections admitted afterwards must
// carry the new generation.
func (c *Conn) Reissue() {
	if c.terminated {
		return
	}
	for _, tr := range c.sections {
		c.release(tr)
	}
	c.sections = make(map[uint64]*tracked)
	c.waiters = 0
	c.waitBytes = 0
	c.table.Reissue()
}
