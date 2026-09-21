package frameq

import (
	"fmt"
	"sort"
)

// Message is a unit of payload to be sent, possibly across many frames.
type Message struct {
	ID       uint32
	Priority int // higher is more urgent
	Data     []byte

	sent int // bytes already emitted in frames; irrevocable once sent
}

// Remaining returns the number of unsent bytes.
func (m *Message) Remaining() int { return len(m.Data) - m.sent }

// Sent returns the number of bytes already emitted into frames.
func (m *Message) Sent() int { return m.sent }

// Scheduler packs fragments of pending messages into frames of at most
// maxFrame bytes (frame header included). Selection is by priority with
// a per-frame fair quota per priority class: in every frame each active
// class is first served up to its quantum, so a large high-priority
// message can never permanently starve smaller lower-priority ones.
// Leftover budget is then distributed work-conservingly, highest
// priority first.
type Scheduler struct {
	maxFrame    int
	quantum     map[int]int
	queues      map[int][]*Message
	priorities  []int // descending
	index       map[uint32]*Message
	pendingByte int
}

// NewScheduler creates a Scheduler. maxFrame is the hard frame size
// limit including FrameHeaderSize. quantum maps priority to the number
// of payload bytes that priority class is guaranteed per frame before
// any class gets a second share.
func NewScheduler(maxFrame int, quantum map[int]int) (*Scheduler, error) {
	if maxFrame < FrameHeaderSize+FragHeaderSize+1 {
		return nil, fmt.Errorf("frameq: maxFrame %d too small to carry one byte of payload", maxFrame)
	}
	s := &Scheduler{
		maxFrame: maxFrame,
		quantum:  make(map[int]int, len(quantum)),
		queues:   make(map[int][]*Message),
		index:    make(map[uint32]*Message),
	}
	for p, q := range quantum {
		if q <= 0 {
			return nil, fmt.Errorf("frameq: quantum for priority %d must be positive", p)
		}
		s.quantum[p] = q
		s.priorities = append(s.priorities, p)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(s.priorities)))
	return s, nil
}

// Enqueue queues a message for sending.
func (s *Scheduler) Enqueue(m *Message) error {
	if _, ok := s.quantum[m.Priority]; !ok {
		return fmt.Errorf("frameq: unknown priority %d", m.Priority)
	}
	if _, ok := s.index[m.ID]; ok {
		return fmt.Errorf("frameq: duplicate message id %d", m.ID)
	}
	s.queues[m.Priority] = append(s.queues[m.Priority], m)
	s.index[m.ID] = m
	s.pendingByte += m.Remaining()
	return nil
}

// Cancel drops the unsent remainder of a message, releasing the quota
// its remaining bytes would have consumed. Bytes already emitted in
// frames cannot be revoked; Cancel returns false once the message has
// been fully sent (or was never enqueued).
func (s *Scheduler) Cancel(id uint32) bool {
	m, ok := s.index[id]
	if !ok {
		return false
	}
	q := s.queues[m.Priority]
	for i, cand := range q {
		if cand == m {
			s.queues[m.Priority] = append(q[:i], q[i+1:]...)
			break
		}
	}
	s.pendingByte -= m.Remaining()
	delete(s.index, id)
	return true
}

// Pending returns the total number of unsent payload bytes.
func (s *Scheduler) Pending() int { return s.pendingByte }

// NextFrame builds the next frame, or returns false when nothing is
// pending. A frame is emitted only when it carries at least one payload
// byte, so data that exactly fills a frame never triggers an extra
// empty frame. The encoded frame never exceeds maxFrame bytes.
func (s *Scheduler) NextFrame() (*Frame, bool) {
	if s.pendingByte == 0 {
		return nil, false
	}
	budget := s.maxFrame - FrameHeaderSize
	frame := &Frame{}
	// Fair pass: every active priority class gets up to its quantum
	// before any class gets more, which bounds starvation.
	for _, prio := range s.priorities {
		s.drain(prio, s.quantum[prio], &budget, frame)
	}
	// Work-conserving passes: spend leftover budget, highest priority first.
	for budget >= FragHeaderSize+1 && s.pendingByte > 0 {
		before := budget
		for _, prio := range s.priorities {
			s.drain(prio, budget, &budget, frame)
		}
		if budget == before {
			break
		}
	}
	if len(frame.Fragments) == 0 {
		return nil, false
	}
	return frame, true
}

// drain emits fragments from the head of one priority queue, spending at
// most limit payload bytes and at most *budget frame-body bytes.
func (s *Scheduler) drain(prio, limit int, budget *int, frame *Frame) {
	spent := 0
	for {
		q := s.queues[prio]
		if len(q) == 0 || spent >= limit || *budget < FragHeaderSize+1 {
			return
		}
		m := q[0]
		n := min(*budget-FragHeaderSize, limit-spent, m.Remaining())
		if n <= 0 {
			return
		}
		frag := Fragment{
			MsgID:  m.ID,
			Offset: uint32(m.sent),
			Total:  uint32(len(m.Data)),
			Data:   m.Data[m.sent : m.sent+n],
		}
		m.sent += n
		s.pendingByte -= n
		if m.Remaining() == 0 {
			frag.Last = true
			s.queues[prio] = q[1:]
			delete(s.index, m.ID)
		}
		spent += n
		*budget -= FragHeaderSize + n
		frame.Fragments = append(frame.Fragments, frag)
	}
}
