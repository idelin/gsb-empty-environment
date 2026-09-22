package framepack

import "errors"

var (
	// ErrNoSuchMessage is returned when canceling an unknown or already
	// fully sent message ID.
	ErrNoSuchMessage = errors.New("framepack: no such message")
	// ErrQuotaExceeded is returned when a message would exceed its
	// priority class queue quota. It is checked before any allocation.
	ErrQuotaExceeded = errors.New("framepack: queue quota exceeded")
	// ErrBadPriority is returned for a priority outside the configured
	// class range.
	ErrBadPriority = errors.New("framepack: bad priority")
	// ErrFrameTooSmall is returned when maxFrame cannot fit a header plus
	// at least one payload byte.
	ErrFrameTooSmall = errors.New("framepack: maxFrame too small for header")
)

// Message is a queued message. Payload must not be mutated while queued.
type Message struct {
	ID       uint32
	Priority int
	Payload  []byte

	sent int // bytes already emitted in frames; irrevocable
}

func (m *Message) remaining() int { return len(m.Payload) - m.sent }

type classQ struct {
	quantum   int // payload bytes this class may send per round
	allowance int // remaining payload bytes in the current round
	quota     int // max unsent bytes buffered in this class
	used      int // unsent bytes currently buffered
	queue     []*Message
}

func (c *classQ) head() *Message {
	if len(c.queue) == 0 {
		return nil
	}
	return c.queue[0]
}

// Scheduler packs messages into frames. Classes are served strictly in
// priority order, but each class may only spend its per-round quantum
// before the next class gets a turn, so a large high-priority message
// cannot permanently starve lower classes.
type Scheduler struct {
	maxFrame int
	capacity int // payload bytes per frame: maxFrame minus header overhead
	classes  []*classQ
	nextID   uint32
	live     map[uint32]*Message
}

// NewScheduler creates a scheduler. maxFrame includes the header. quanta[i]
// is the per-round payload budget of class i (class 0 is highest priority).
// queueQuota caps unsent buffered bytes per class.
func NewScheduler(maxFrame int, quanta []int, queueQuota int) (*Scheduler, error) {
	if maxFrame < HeaderSize+1 {
		return nil, ErrFrameTooSmall
	}
	if len(quanta) == 0 {
		return nil, ErrBadPriority
	}
	classes := make([]*classQ, len(quanta))
	for i, q := range quanta {
		if q < 1 {
			q = 1
		}
		classes[i] = &classQ{quantum: q, allowance: q, quota: queueQuota}
	}
	return &Scheduler{
		maxFrame: maxFrame,
		capacity: maxFrame - HeaderSize,
		classes:  classes,
		live:     make(map[uint32]*Message),
	}, nil
}

// Enqueue queues payload at the given priority and returns its message ID.
// The class queue quota is checked before the message is retained.
func (s *Scheduler) Enqueue(priority int, payload []byte) (uint32, error) {
	if priority < 0 || priority >= len(s.classes) {
		return 0, ErrBadPriority
	}
	c := s.classes[priority]
	if c.used+len(payload) > c.quota {
		return 0, ErrQuotaExceeded
	}
	s.nextID++
	m := &Message{ID: s.nextID, Priority: priority, Payload: payload}
	c.queue = append(c.queue, m)
	c.used += len(payload)
	s.live[m.ID] = m
	return m.ID, nil
}

// Cancel drops the unsent remainder of a message and releases its queue
// quota, returning the number of unsent bytes released. Fragments already
// emitted by NextFrame are irrevocable and are not reflected in the return
// value.
func (s *Scheduler) Cancel(id uint32) (released int, err error) {
	m, ok := s.live[id]
	if !ok {
		return 0, ErrNoSuchMessage
	}
	c := s.classes[m.Priority]
	for i, qm := range c.queue {
		if qm == m {
			c.queue = append(c.queue[:i:i], c.queue[i+1:]...)
			break
		}
	}
	released = m.remaining()
	c.used -= released
	delete(s.live, id)
	return released, nil
}

// QueueUsed reports the unsent bytes currently buffered in a class.
func (s *Scheduler) QueueUsed(priority int) int {
	if priority < 0 || priority >= len(s.classes) {
		return 0
	}
	return s.classes[priority].used
}

// NextFrame emits the next frame, or ok=false when nothing is queued. The
// returned frame, including its header, never exceeds maxFrame bytes. A
// message whose remaining bytes exactly fill a frame is finished by that
// frame; no extra empty frame is produced.
func (s *Scheduler) NextFrame() (f Frame, ok bool) {
	for {
		for _, c := range s.classes {
			m := c.head()
			if m == nil || c.allowance <= 0 {
				continue
			}
			n := min(m.remaining(), s.capacity, c.allowance)
			if n <= 0 {
				continue
			}
			var flags byte
			if m.sent == 0 {
				flags |= FlagStart
			}
			start := m.sent
			m.sent += n
			if m.remaining() == 0 {
				flags |= FlagEnd
			}
			c.allowance -= n
			c.used -= n
			if m.remaining() == 0 {
				c.queue = c.queue[1:]
				delete(s.live, m.ID)
			}
			return Frame{
				MsgID:    m.ID,
				Priority: uint8(m.Priority),
				Flags:    flags,
				TotalLen: uint32(len(m.Payload)),
				Payload:  m.Payload[start:m.sent],
			}, true
		}
		// No class could send: either everything is drained, or every
		// nonempty class spent its round allowance. Reset allowances so
		// lower classes are never starved for more than one round.
		nonempty := false
		for _, c := range s.classes {
			if len(c.queue) > 0 {
				nonempty = true
			}
			c.allowance = c.quantum
		}
		if !nonempty {
			return Frame{}, false
		}
	}
}
