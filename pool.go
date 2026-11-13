package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrPoolExhausted means no free KV slot is available.
	ErrPoolExhausted = errors.New("netsec: KV block pool exhausted")
	// ErrStaleBlock means a block handle belongs to an older slot generation.
	ErrStaleBlock = errors.New("netsec: stale KV block generation")
	// ErrBlockNotFilling means Fill was called on a block that is no longer
	// reserved for filling. A ready entry is immutable.
	ErrBlockNotFilling = errors.New("netsec: KV block is not reserved for filling")
	// ErrBlockNotReady means a block has been reserved but not filled.
	ErrBlockNotReady = errors.New("netsec: KV block is not ready")
	// ErrInvalidBlock means the handle does not name a slot in this pool.
	ErrInvalidBlock = errors.New("netsec: invalid KV block handle")
)

// BlockHandle names one immutable KV block. Generation prevents a late fill
// from writing through a handle after its slot has been recycled.
type BlockHandle struct {
	Slot       uint32
	Generation uint64
}

// SlotState is the lifecycle state of a pool slot.
type SlotState uint8

const (
	SlotFree SlotState = iota
	SlotFilling
	SlotReady
)

func (s SlotState) String() string {
	switch s {
	case SlotFree:
		return "free"
	case SlotFilling:
		return "filling"
	case SlotReady:
		return "ready"
	default:
		return fmt.Sprintf("state(%d)", uint8(s))
	}
}

// BlockInfo is metadata for a pool slot. It intentionally does not expose the
// block contents. AllZero lets a host verify that secret bytes were scrubbed
// before the slot was returned to the pool.
type BlockInfo struct {
	Slot       uint32
	Generation uint64
	State      SlotState
	RefCount   int
	Size       int
	AllZero    bool
}

type poolSlot struct {
	generation uint64
	state      SlotState
	refs       int
	data       []byte
}

// Pool owns the KV block storage. Blocks become immutable after Fill and are
// zeroed when their final reference is released.
type Pool struct {
	mu    sync.Mutex
	slots []poolSlot
}

// NewPool creates a pool with the given number of fixed-size slots.
func NewPool(slots int) (*Pool, error) {
	if slots <= 0 {
		return nil, errors.New("netsec: block pool size must be positive")
	}
	if slots > 1<<24 {
		return nil, errors.New("netsec: block pool size is unreasonable")
	}
	return &Pool{slots: make([]poolSlot, slots)}, nil
}

// Size returns the number of slots in the pool.
func (p *Pool) Size() int { return len(p.slots) }

// Allocate reserves a free slot for filling. The returned handle carries a new
// generation. The reservation owns one reference until Fill+transfer or
// Release.
func (p *Pool) Allocate() (BlockHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.slots {
		s := &p.slots[i]
		if s.state != SlotFree {
			continue
		}
		if s.generation == ^uint64(0) {
			return BlockHandle{}, errors.New("netsec: KV block generation exhausted")
		}
		s.generation++
		s.state = SlotFilling
		s.refs = 1
		// Keep the buffer for reuse, but it must already be zero. This also
		// defends against a partially written buffer from an aborted fill.
		full := s.data[:cap(s.data)]
		for j := range full {
			full[j] = 0
		}
		return BlockHandle{Slot: uint32(i), Generation: s.generation}, nil
	}
	return BlockHandle{}, ErrPoolExhausted
}

func (p *Pool) slotFor(h BlockHandle) (*poolSlot, error) {
	if int(h.Slot) >= len(p.slots) || h.Generation == 0 {
		return nil, ErrInvalidBlock
	}
	s := &p.slots[h.Slot]
	if s.generation != h.Generation {
		return nil, ErrStaleBlock
	}
	return s, nil
}

// Fill writes data into a reserved slot. It fails if the handle's generation
// is stale or if the slot has already been sealed. A late fill therefore
// cannot overwrite a newer entry that reused the same slot.
func (p *Pool) Fill(h BlockHandle, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.slotFor(h)
	if err != nil {
		return err
	}
	if s.state != SlotFilling {
		return ErrBlockNotFilling
	}
	s.data = append(s.data[:0], data...)
	s.state = SlotReady
	return nil
}

// Pin adds one reference to a ready block.
func (p *Pool) Pin(h BlockHandle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.slotFor(h)
	if err != nil {
		return err
	}
	if s.state != SlotReady {
		return ErrBlockNotReady
	}
	s.refs++
	return nil
}

// Release drops one reference. When the final reference is dropped the bytes
// are zeroed in place and only then is the slot made free for reuse.
func (p *Pool) Release(h BlockHandle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.slotFor(h)
	if err != nil {
		return err
	}
	if s.state != SlotFilling && s.state != SlotReady {
		return ErrStaleBlock
	}
	if s.refs <= 0 {
		return errors.New("netsec: KV block reference underflow")
	}
	s.refs--
	if s.refs == 0 {
		full := s.data[:cap(s.data)]
		for i := range full {
			full[i] = 0
		}
		s.state = SlotFree
	}
	return nil
}

// Read returns a copy of a ready block's data. Callers must not treat this as
// a way to mutate the cache.
func (p *Pool) Read(h BlockHandle) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.slotFor(h)
	if err != nil {
		return nil, err
	}
	if s.state != SlotReady {
		return nil, ErrBlockNotReady
	}
	out := make([]byte, len(s.data))
	copy(out, s.data)
	return out, nil
}

// SlotInfo returns metadata for a slot, including whether its retained buffer
// is currently all zero. It is useful for leak and scrubbing tests without
// revealing block contents.
func (p *Pool) SlotInfo(slot uint32) (BlockInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if int(slot) >= len(p.slots) {
		return BlockInfo{}, ErrInvalidBlock
	}
	s := &p.slots[slot]
	allZero := true
	for _, b := range s.data {
		if b != 0 {
			allZero = false
			break
		}
	}
	return BlockInfo{
		Slot:       slot,
		Generation: s.generation,
		State:      s.state,
		RefCount:   s.refs,
		Size:       len(s.data),
		AllZero:    allZero,
	}, nil
}

func putUint64(dst []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		dst[i] = byte(v)
		v >>= 8
	}
}

// allBlockHandles returns each distinct handle in an entry exactly once.
func allBlockHandles(layers []LayerKV) []BlockHandle {
	seen := make(map[BlockHandle]struct{})
	var out []BlockHandle
	add := func(h BlockHandle) {
		if _, ok := seen[h]; ok {
			return
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	for _, layer := range layers {
		for _, seg := range layer.Segments {
			add(seg.K)
			add(seg.V)
		}
	}
	return out
}
