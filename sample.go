package layout

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
)

const checkpointVersion = 1

// BitSource is a forward-only stream of random bits. NextBit must return
// exactly 0 or 1. Returning ErrNeedMoreBits pauses sampling without changing
// the sampler state; the caller may checkpoint and later resume with a source
// positioned at the checkpoint's BitPosition.
type BitSource interface {
	NextBit() (uint8, error)
}

// PositionedBitSource is an optional interface for sources that can report
// how many bits have been consumed. Resume checks it when available.
type PositionedBitSource interface {
	BitSource
	Position() uint64
}

// BitFunc adapts a function to BitSource.
type BitFunc func() (uint8, error)

// NextBit calls f.
func (f BitFunc) NextBit() (uint8, error) { return f() }

// SliceSource is a forward-only bit source backed by a byte slice. Bits are
// read most-significant-bit first within each byte.
type SliceSource struct {
	data []byte
	pos  uint64
}

// NewSliceSource starts at bit zero.
func NewSliceSource(data []byte) *SliceSource {
	return &SliceSource{data: data}
}

// NewSliceSourceAt starts at the given absolute bit offset. It does not
// rewind or seek afterwards.
func NewSliceSourceAt(data []byte, bitOffset uint64) *SliceSource {
	return &SliceSource{data: data, pos: bitOffset}
}

// NextBit implements BitSource.
func (s *SliceSource) NextBit() (uint8, error) {
	byteIndex := s.pos / 8
	if byteIndex >= uint64(len(s.data)) {
		return 0, ErrNeedMoreBits
	}
	shift := uint(7 - (s.pos % 8))
	bit := (s.data[byteIndex] >> shift) & 1
	s.pos++
	return bit, nil
}

// Position returns the absolute next-bit offset.
func (s *SliceSource) Position() uint64 { return s.pos }

// Sampler is a resumable uniform sampler. It draws one uniform integer in
// [0, Count()) by rejection sampling and then unranks it. The sampler never
// enumerates the layout catalogue.
type Sampler struct {
	p     *Planner
	src   BitSource
	total *big.Int
	k     int

	consumed uint64
	value    *big.Int
	bits     int

	done   bool
	layout Layout
}

// NewSampler creates a sampler over src. The planner's Count must be
// non-zero.
func (p *Planner) NewSampler(src BitSource) (*Sampler, error) {
	if src == nil {
		return nil, fmt.Errorf("layout: nil bit source")
	}
	total := p.Count()
	if total.Sign() == 0 {
		return nil, ErrNoLayouts
	}
	k := new(big.Int).Sub(total, big.NewInt(1)).BitLen()
	return &Sampler{
		p:     p,
		src:   src,
		total: total,
		k:     k,
		value: new(big.Int),
	}, nil
}

// Sample draws one layout using src. If the source pauses, Sample returns
// ErrNeedMoreBits and discards the sampler state; use NewSampler + Draw when
// pause/resume is required.
func (p *Planner) Sample(src BitSource) (Layout, error) {
	s, err := p.NewSampler(src)
	if err != nil {
		return Layout{}, err
	}
	return s.Draw()
}

// Draw continues sampling until a layout is produced or the bit source asks
// for more bits. On ErrNeedMoreBits, the sampler state is unchanged and may be
// checkpointed and resumed.
func (s *Sampler) Draw() (Layout, error) {
	if s.done {
		return s.layout.Clone(), nil
	}

	var rank *big.Int
	if s.k == 0 {
		rank = big.NewInt(0)
	} else {
		for {
			if s.bits == s.k {
				if s.value.Cmp(s.total) < 0 {
					rank = new(big.Int).Set(s.value)
					break
				}
				// Rejection: values in [Count, 2^k) are discarded.
				s.value.SetInt64(0)
				s.bits = 0
			}
			bit, err := s.nextBit()
			if err != nil {
				return Layout{}, err
			}
			s.value.Lsh(s.value, 1)
			if bit == 1 {
				s.value.SetBit(s.value, 0, 1)
			}
			s.bits++
		}
	}

	l, err := s.p.Unrank(rank)
	if err != nil {
		return Layout{}, err
	}
	s.done = true
	s.layout = l.Clone()
	return l, nil
}

func (s *Sampler) nextBit() (uint8, error) {
	bit, err := s.src.NextBit()
	if err != nil {
		return 0, err
	}
	if bit > 1 {
		return 0, fmt.Errorf("layout: bit source returned %d, want 0 or 1", bit)
	}
	s.consumed++
	return bit, nil
}

// BitPosition returns the number of bits consumed by this sampler.
func (s *Sampler) BitPosition() uint64 { return s.consumed }

// Done reports whether Draw has produced the final layout.
func (s *Sampler) Done() bool { return s.done }

// Checkpoint serializes the sampler's exact position in the random stream.
func (s *Sampler) Checkpoint() (Checkpoint, error) {
	if s == nil || s.p == nil {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	cp := Checkpoint{
		version:    checkpointVersion,
		configHash: s.p.hash,
		consumed:   s.consumed,
		bits:       s.bits,
		value:      append([]byte(nil), s.value.Bytes()...),
		done:       s.done,
	}
	if s.done {
		cp.layout = append([]int(nil), s.layout.Starts...)
	}
	return cp, nil
}

// Checkpoint is an opaque, serializable snapshot of a sampler. It is bound to
// the complete normalized configuration, including constraints and lease
// generations.
type Checkpoint struct {
	version    int
	configHash [32]byte
	consumed   uint64
	bits       int
	value      []byte
	done       bool
	layout     []int
}

// BitPosition returns the absolute bit offset at which the source must resume.
func (c Checkpoint) BitPosition() uint64 { return c.consumed }

// Done reports whether the checkpoint already contains a final layout.
func (c Checkpoint) Done() bool { return c.done }

// Layout returns the final layout when the checkpoint is done.
func (c Checkpoint) Layout() (Layout, bool) {
	if !c.done {
		return Layout{}, false
	}
	return Layout{Starts: append([]int(nil), c.layout...)}, true
}

type checkpointWire struct {
	Version    int    `json:"version"`
	ConfigHash string `json:"configHash"`
	Consumed   uint64 `json:"consumed"`
	Bits       int    `json:"bits"`
	Value      []byte `json:"value"`
	Done       bool   `json:"done"`
	Layout     []int  `json:"layout,omitempty"`
}

// MarshalBinary serializes the checkpoint.
func (c Checkpoint) MarshalBinary() ([]byte, error) {
	if c.version != checkpointVersion {
		return nil, ErrInvalidCheckpoint
	}
	w := checkpointWire{
		Version:    c.version,
		ConfigHash: hex.EncodeToString(c.configHash[:]),
		Consumed:   c.consumed,
		Bits:       c.bits,
		Value:      append([]byte(nil), c.value...),
		Done:       c.done,
		Layout:     append([]int(nil), c.layout...),
	}
	return json.Marshal(w)
}

// UnmarshalBinary restores a serialized checkpoint.
func (c *Checkpoint) UnmarshalBinary(data []byte) error {
	var w checkpointWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCheckpoint, err)
	}
	if w.Version != checkpointVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidCheckpoint, w.Version)
	}
	hash, err := hex.DecodeString(w.ConfigHash)
	if err != nil || len(hash) != 32 {
		return fmt.Errorf("%w: bad configuration hash", ErrInvalidCheckpoint)
	}
	if w.Bits < 0 {
		return fmt.Errorf("%w: negative bit count", ErrInvalidCheckpoint)
	}
	c.version = w.Version
	copy(c.configHash[:], hash)
	c.consumed = w.Consumed
	c.bits = w.Bits
	c.value = append([]byte(nil), w.Value...)
	c.done = w.Done
	c.layout = append([]int(nil), w.Layout...)
	return nil
}

// Resume restores a sampler from a checkpoint. The source must be positioned
// at cp.BitPosition(); if it implements PositionedBitSource this is checked.
// A checkpoint is rejected with ErrStaleCheckpoint if constraints, leases,
// lease generations, regions, or any other part of the configuration changed.
func (p *Planner) Resume(cp Checkpoint, src BitSource) (*Sampler, error) {
	if cp.version != checkpointVersion {
		return nil, ErrInvalidCheckpoint
	}
	if cp.configHash != p.hash {
		return nil, ErrStaleCheckpoint
	}
	if src == nil {
		return nil, fmt.Errorf("layout: nil bit source")
	}
	if ps, ok := src.(PositionedBitSource); ok && ps.Position() != cp.consumed {
		return nil, ErrBitPosition
	}
	total := p.Count()
	if total.Sign() == 0 {
		return nil, ErrNoLayouts
	}
	k := new(big.Int).Sub(total, big.NewInt(1)).BitLen()

	s := &Sampler{
		p:        p,
		src:      src,
		total:    total,
		k:        k,
		consumed: cp.consumed,
		bits:     cp.bits,
		value:    new(big.Int).SetBytes(cp.value),
		done:     cp.done,
	}
	if cp.done {
		s.layout = Layout{Starts: append([]int(nil), cp.layout...)}
		if err := p.Validate(s.layout); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCheckpoint, err)
		}
		return s, nil
	}
	if cp.bits < 0 || cp.bits > k {
		return nil, fmt.Errorf("%w: bit count %d outside [0,%d]", ErrInvalidCheckpoint, cp.bits, k)
	}
	if s.value.BitLen() > cp.bits {
		return nil, fmt.Errorf("%w: value does not fit in %d bits", ErrInvalidCheckpoint, cp.bits)
	}
	return s, nil
}
