package fragmentreassembly

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrMessageTooLarge = errors.New("message larger than per-message quota")
	ErrQuotaExceeded   = errors.New("global buffer quota exceeded")
)

// Config controls reassembly buffering. Zero means unlimited for size and
// quota fields; a zero TTL disables expiration.
type Config struct {
	MaxMessageSize int
	GlobalBytes    int
	TTL            time.Duration
}

// Result describes an Add call. Data is populated only when Complete is true
// and is a copy owned by the caller.
type Result struct {
	MessageID uint64
	Complete  bool
	Duplicate bool
	Data      []byte
}

type activeMessage struct {
	total    uint32
	lastSeen time.Time
	data     []byte
	ranges   [][2]uint32
}

type completedMessage struct {
	total    uint32
	lastSeen time.Time
	data     []byte
}

// Reassembler reassembles fragments keyed by message ID. It is not safe for
// concurrent use; callers needing concurrency should add their own mutex.
type Reassembler struct {
	cfg       Config
	active    map[uint64]*activeMessage
	completed map[uint64]*completedMessage
}

func NewReassembler(cfg Config) (*Reassembler, error) {
	if cfg.MaxMessageSize < 0 || cfg.GlobalBytes < 0 || cfg.TTL < 0 {
		return nil, errors.New("limits and TTL must not be negative")
	}
	return &Reassembler{
		cfg:       cfg,
		active:    make(map[uint64]*activeMessage),
		completed: make(map[uint64]*completedMessage),
	}, nil
}

// Allow is suitable for ReadFrame's gate and checks only length quotas before
// a payload buffer is allocated.
func (r *Reassembler) Allow(header Header) error {
	return r.AllowAt(header, time.Now())
}

// AllowAt is Allow with an injectable clock for tests.
func (r *Reassembler) AllowAt(header Header, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	r.purge(at)
	if done, ok := r.completed[header.MessageID]; ok {
		if done.total != header.TotalSize {
			return fmt.Errorf("%w: message %d total size changed", ErrConflict, header.MessageID)
		}
		return nil
	}
	if state, ok := r.active[header.MessageID]; ok {
		if state.total != header.TotalSize {
			return fmt.Errorf("%w: message %d total size changed", ErrConflict, header.MessageID)
		}
		return nil
	}
	return r.reserveAllowed(header.TotalSize)
}

// Add stores one validated fragment and reports completion or duplicate
// completion. Overlapping bytes must be identical; otherwise ErrConflict is
// returned and state is left unchanged.
func (r *Reassembler) Add(frag Fragment) (Result, error) {
	return r.AddAt(frag, time.Now())
}

// AddAt is Add with an injectable clock for expiration tests.
func (r *Reassembler) AddAt(frag Fragment, at time.Time) (Result, error) {
	if err := frag.validateLengths(); err != nil {
		return Result{}, err
	}
	if at.IsZero() {
		at = time.Now()
	}
	r.purge(at)

	id := frag.MessageID
	if done, ok := r.completed[id]; ok {
		if done.total != frag.TotalSize {
			return Result{}, fmt.Errorf("%w: message %d total size changed", ErrConflict, id)
		}
		end := int(frag.Offset) + len(frag.Payload)
		if end > len(done.data) {
			return Result{}, fmt.Errorf("%w: message %d fragment outside completed message", ErrConflict, id)
		}
		for i, b := range frag.Payload {
			if done.data[int(frag.Offset)+i] != b {
				return Result{}, fmt.Errorf("%w: message %d byte %d differs", ErrConflict, id, int(frag.Offset)+i)
			}
		}
		return Result{
			MessageID: id,
			Complete:  true,
			Duplicate: true,
			Data:      append([]byte(nil), done.data...),
		}, nil
	}

	state, ok := r.active[id]
	if !ok {
		if err := r.reserveAllowed(frag.TotalSize); err != nil {
			return Result{}, err
		}
		state = &activeMessage{
			total:    frag.TotalSize,
			lastSeen: at,
			data:     make([]byte, frag.TotalSize),
		}
		r.active[id] = state
	}
	if state.total != frag.TotalSize {
		return Result{}, fmt.Errorf("%w: message %d total size changed", ErrConflict, id)
	}

	start := int(frag.Offset)
	end := start + len(frag.Payload)
	for _, covered := range state.ranges {
		overlapStart := max(start, int(covered[0]))
		overlapEnd := min(end, int(covered[1]))
		for pos := overlapStart; pos < overlapEnd; pos++ {
			incoming := frag.Payload[pos-start]
			if state.data[pos] != incoming {
				return Result{}, fmt.Errorf("%w: message %d byte %d differs", ErrConflict, id, pos)
			}
		}
	}

	copy(state.data[start:end], frag.Payload)
	state.ranges = appendRange(state.ranges, frag.Offset, uint32(end))
	state.lastSeen = at

	covered := coveredBytes(state.ranges)
	if covered != int(state.total) {
		return Result{MessageID: id}, nil
	}

	data := state.data
	delete(r.active, id)
	r.completed[id] = &completedMessage{
		total:    state.total,
		lastSeen: at,
		data:     data,
	}
	return Result{
		MessageID: id,
		Complete:  true,
		Data:      append([]byte(nil), data...),
	}, nil
}

func (r *Reassembler) reserveAllowed(total uint32) error {
	if r.cfg.MaxMessageSize != 0 && int(total) > r.cfg.MaxMessageSize {
		return fmt.Errorf("%w: total=%d limit=%d", ErrMessageTooLarge, total, r.cfg.MaxMessageSize)
	}
	if r.cfg.GlobalBytes != 0 && r.reservedBytes()+int(total) > r.cfg.GlobalBytes {
		return fmt.Errorf("%w: need=%d used=%d limit=%d", ErrQuotaExceeded, total, r.reservedBytes(), r.cfg.GlobalBytes)
	}
	return nil
}

func (r *Reassembler) reservedBytes() int {
	used := 0
	for _, msg := range r.active {
		used += int(msg.total)
	}
	for _, msg := range r.completed {
		used += len(msg.data)
	}
	return used
}

func (r *Reassembler) purge(at time.Time) {
	if r.cfg.TTL == 0 {
		return
	}
	for id, msg := range r.active {
		if !at.Before(msg.lastSeen.Add(r.cfg.TTL)) {
			delete(r.active, id)
		}
	}
	for id, msg := range r.completed {
		if !at.Before(msg.lastSeen.Add(r.cfg.TTL)) {
			delete(r.completed, id)
		}
	}
}

func appendRange(ranges [][2]uint32, start, end uint32) [][2]uint32 {
	merged := append(ranges, [2]uint32{start, end})
	sort.Slice(merged, func(i, j int) bool {
		return merged[i][0] < merged[j][0]
	})

	out := merged[:0]
	for _, current := range merged {
		if len(out) == 0 || current[0] > out[len(out)-1][1] {
			out = append(out, current)
			continue
		}
		if current[1] > out[len(out)-1][1] {
			out[len(out)-1][1] = current[1]
		}
	}
	return out
}

func coveredBytes(ranges [][2]uint32) int {
	total := 0
	for _, r := range ranges {
		total += int(r[1] - r[0])
	}
	return total
}
