package netsec

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// Config configures a shaped output encoder.
type Config struct {
	Plan     Plan
	Schedule Schedule
	Round    uint32
	Key      *Key
	Clock    Clock
}

// Frame is one externally visible slot. Data is AES-GCM ciphertext and is
// always exactly Plan.SlotBytes long, regardless of task progress.
type Frame struct {
	Round uint32
	Slot  uint64
	// SendAt is the public schedule time for this slot. The host is
	// responsible for actually sending frames at these times.
	SendAt time.Time
	Data   []byte
}

// Encoder turns task progress into a fixed public frame trajectory.
//
// It is safe for one producer to call Submit/Finish concurrently with one
// consumer calling EmitNext.
type Encoder struct {
	mu sync.Mutex

	plan     Plan
	schedule Schedule
	round    uint32
	key      *Key
	clock    Clock
	deadline time.Time

	pending   []byte
	total     uint64
	encodeLen uint64
	dataStart uint64
	firstData uint64
	nextData  uint64
	next      uint64

	finished bool
	success  bool
	late     bool
	closed   bool
}

// NewEncoder validates the configuration, claims the (key, round) pair, and
// creates an encoder. A nil Clock uses SystemClock.
func NewEncoder(cfg Config) (*Encoder, error) {
	if err := cfg.Plan.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Schedule.validateFor(cfg.Plan); err != nil {
		return nil, err
	}
	if cfg.Key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidKey)
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock{}
	}
	if err := cfg.Key.claimRound(cfg.Round); err != nil {
		return nil, err
	}

	return &Encoder{
		plan:     cfg.Plan,
		schedule: cfg.Schedule,
		round:    cfg.Round,
		key:      cfg.Key,
		clock:    cfg.Clock,
		deadline: cfg.Schedule.SlotTime(cfg.Plan.Slots - 1),
	}, nil
}

// New is a convenience wrapper around NewEncoder.
func New(plan Plan, schedule Schedule, round uint32, key *Key, clock Clock) (*Encoder, error) {
	return NewEncoder(Config{
		Plan:     plan,
		Schedule: schedule,
		Round:    round,
		Key:      key,
		Clock:    clock,
	})
}

// Submit queues secret result bytes. The public (N-1)*L bound and the actual
// authenticated-frame capacity are both enforced. Bytes are laid out only
// after completion: the last data slots needed are filled, with the first
// partial slot right-aligned, so padding can never create gaps in recovery.
//
// Submit does not wait for or reveal the task's completion time.
func (e *Encoder) Submit(data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return fmt.Errorf("%w: cannot submit after the final slot", ErrEmitted)
	}
	if e.finished {
		return ErrFinished
	}
	if len(data) == 0 {
		return nil
	}

	incoming := uint64(len(data))
	if incoming > e.plan.ResultLimit()-e.total {
		return fmt.Errorf("%w: %d > public limit %d", ErrTooLarge, e.total+incoming, e.plan.ResultLimit())
	}
	if incoming > e.plan.EncodableBytes()-e.total {
		return fmt.Errorf("%w: %d > authenticated-frame capacity %d", ErrTooLarge, e.total+incoming, e.plan.EncodableBytes())
	}

	e.pending = append(e.pending, data...)
	e.total += incoming
	return nil
}

// Write implements io.Writer for producers that already use a writer.
func (e *Encoder) Write(p []byte) (int, error) {
	if err := e.Submit(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Finish records the task's final status. It must be called before the last
// scheduled slot and with enough data slots left to carry the result. A late
// call, or one that cannot place the result, records failure and returns
// ErrLate; the host must still emit every remaining frame.
func (e *Encoder) Finish(success bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrLate
	}
	if e.finished {
		return ErrAlreadyFinished
	}

	missedDeadline := !e.clock.Now().Before(e.deadline)
	if missedDeadline {
		e.late = true
		success = false
	}
	e.finished = true
	e.success = success
	if !e.layoutResult() {
		e.late = true
		return ErrLate
	}
	if missedDeadline {
		return ErrLate
	}
	return nil
}

// Deadline is the start of the final public slot.
func (e *Encoder) Deadline() time.Time {
	return e.deadline
}

// EmitNext emits the next planned slot. It always returns exactly one
// Plan.SlotBytes frame until the plan is exhausted. A task that has not
// finished by the final slot is encoded as a failure.
func (e *Encoder) EmitNext() (Frame, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return Frame{}, ErrEmitted
	}

	slot := e.next
	plaintext := make([]byte, e.plan.DataBytesPerSlot())
	isFinal := slot == e.plan.Slots-1

	if isFinal {
		if !e.finished {
			e.finished = true
			e.success = false
			e.late = true
			e.layoutResult()
		}
		metadataLength := e.encodeLen
		if e.nextData != e.encodeLen {
			// Defensive invariant: never overstate what was transported while
			// preserving the encrypted final metadata under failure.
			metadataLength = e.nextData
			e.success = false
			e.late = true
		}
		if metadataLength != e.total {
			e.success = false
		}
		binary.BigEndian.PutUint64(plaintext[0:8], metadataLength)
		if e.success {
			plaintext[8] = 1
		}
		e.closed = true
	} else {
		if e.finished && slot >= e.dataStart && e.nextData < e.encodeLen {
			if slot == e.dataStart {
				copy(plaintext[len(plaintext)-int(e.firstData):], e.pending[:e.firstData])
				e.nextData = e.firstData
			} else {
				n := uint64(len(plaintext))
				if remaining := e.encodeLen - e.nextData; remaining < n {
					n = remaining
				}
				copy(plaintext, e.pending[e.nextData:e.nextData+n])
				e.nextData += n
			}
		}
	}

	nonce := NonceFor(e.round, slot)
	aad := AssociatedData(e.plan, e.round, slot)
	ciphertext, err := e.key.seal(nonce, plaintext, aad)
	if err != nil {
		return Frame{}, err
	}
	if len(ciphertext) != e.plan.SlotBytes {
		return Frame{}, fmt.Errorf(
			"%w: encoded frame is %d bytes, want %d",
			ErrFrameLayout,
			len(ciphertext),
			e.plan.SlotBytes,
		)
	}

	frame := Frame{
		Round:  e.round,
		Slot:   slot,
		SendAt: e.schedule.SlotTime(slot),
		Data:   ciphertext,
	}
	e.next++
	return frame, nil
}

// Status is a local view of completion. It must not be included in external
// frame metadata before the final encrypted slot.
type Status struct {
	Finished bool
	Success  bool
	Late     bool
	Length   uint64
}

// Status returns the encoder's local task status.
func (e *Encoder) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Status{
		Finished: e.finished,
		Success:  e.success && e.finished && !e.late,
		Late:     e.late,
		Length:   e.total,
	}
}

// layoutResult places the result inside the final data slots. The first used
// data slot is right-aligned and later slots are full, so OpenFrames can
// recover the result by taking the final length bytes of the data-slot stream.
func (e *Encoder) layoutResult() bool {
	e.encodeLen = 0
	e.dataStart = e.plan.Slots - 1
	e.firstData = 0
	if e.plan.Slots <= 1 || e.total == 0 {
		return true
	}

	needed := slotsNeeded(e.total, uint64(e.plan.DataBytesPerSlot()))
	available := e.remainingDataSlots()
	if needed > available {
		e.success = false
		return false
	}

	e.encodeLen = e.total
	e.dataStart = e.plan.Slots - 1 - needed
	perSlot := uint64(e.plan.DataBytesPerSlot())
	e.firstData = e.encodeLen % perSlot
	if e.firstData == 0 {
		e.firstData = perSlot
	}
	return true
}

func (e *Encoder) remainingDataSlots() uint64 {
	if e.plan.Slots <= 1 || e.next >= e.plan.Slots-1 {
		return 0
	}
	return e.plan.Slots - 1 - e.next
}

func slotsNeeded(length, perSlot uint64) uint64 {
	if perSlot == 0 || length == 0 {
		return 0
	}
	return 1 + (length-1)/perSlot
}
