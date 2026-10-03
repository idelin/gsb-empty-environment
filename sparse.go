package aries

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
)

// NumCells is the number of uint32 cells per page (4 KiB dense equivalent).
const NumCells = 1024

// Adaptive container thresholds with hysteresis to avoid oscillation.
const (
	densifyThreshold  = NumCells / 4 // sparse -> dense when non-zero cells exceed 25%
	sparsifyThreshold = NumCells / 8 // dense -> sparse when non-zero cells drop below 12.5%
)

// SparsePage is a memory-compact page container for massive sparse data.
// It adaptively converts between a sparse map and a dense array based on
// the live density of non-zero cells, and serializes to an RLE/varint
// compressed byte representation.
type SparsePage struct {
	mu      sync.RWMutex
	LSN     uint64 // pageLSN used by the ARIES redo/undo logic
	dense   bool
	cells   []uint32          // dense representation
	sparse  map[uint32]uint32 // sparse representation (non-zero cells only)
	nonZero int
}

// NewSparsePage returns an empty page using the sparse representation.
func NewSparsePage() *SparsePage {
	return &SparsePage{sparse: make(map[uint32]uint32)}
}

// Get returns the value of the cell at off (0 if unset).
func (p *SparsePage) Get(off uint32) uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.get(off)
}

func (p *SparsePage) get(off uint32) uint32 {
	if p.dense {
		return p.cells[off]
	}
	return p.sparse[off]
}

// Set writes val into the cell at off. A zero value deletes the cell.
// The container converts between sparse and dense forms as needed.
func (p *SparsePage) Set(off uint32, val uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set(off, val)
}

func (p *SparsePage) set(off uint32, val uint32) {
	old := p.get(off)
	if old == val {
		return
	}
	if p.dense {
		p.cells[off] = val
	} else if val == 0 {
		delete(p.sparse, off)
	} else {
		p.sparse[off] = val
	}
	switch {
	case old == 0 && val != 0:
		p.nonZero++
	case old != 0 && val == 0:
		p.nonZero--
	}
	p.adapt()
}

// adapt converts the container representation when density crosses a
// threshold. Hysteresis (densify at 25%, sparsify at 12.5%) prevents
// oscillation for workloads hovering near a single boundary.
func (p *SparsePage) adapt() {
	if !p.dense && p.nonZero > densifyThreshold {
		p.cells = make([]uint32, NumCells)
		for off, v := range p.sparse {
			p.cells[off] = v
		}
		p.sparse = nil
		p.dense = true
	} else if p.dense && p.nonZero < sparsifyThreshold {
		p.sparse = make(map[uint32]uint32, p.nonZero)
		for off, v := range p.cells {
			if v != 0 {
				p.sparse[uint32(off)] = v
			}
		}
		p.cells = nil
		p.dense = false
	}
}

// NonZero reports the number of non-zero cells.
func (p *SparsePage) NonZero() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nonZero
}

// Dense reports whether the page currently uses the dense representation.
func (p *SparsePage) Dense() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dense
}

// MemoryBytes estimates the live heap usage of the container payload.
func (p *SparsePage) MemoryBytes() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.dense {
		return NumCells * 4
	}
	return len(p.sparse) * 12 // key + value + bucket overhead estimate
}

// Serialize encodes the page as [uvarint LSN][mode byte][payload].
// Sparse pages encode as delta-varint offset/value pairs; dense pages
// encode as RLE blocks of (zeroRun, literalCount, literals...).
func (p *SparsePage) Serialize() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	buf := make([]byte, 0, 64)
	buf = binary.AppendUvarint(buf, p.LSN)
	if !p.dense {
		buf = append(buf, 0)
		offs := make([]uint32, 0, len(p.sparse))
		for off := range p.sparse {
			offs = append(offs, off)
		}
		sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
		buf = binary.AppendUvarint(buf, uint64(len(offs)))
		prev := uint64(0)
		for _, off := range offs {
			buf = binary.AppendUvarint(buf, uint64(off)-prev)
			buf = binary.AppendUvarint(buf, uint64(p.sparse[off]))
			prev = uint64(off)
		}
		return buf
	}
	buf = append(buf, 1)
	i := 0
	for i < NumCells {
		zeros := 0
		for i < NumCells && p.cells[i] == 0 {
			zeros++
			i++
		}
		litStart := i
		for i < NumCells && p.cells[i] != 0 {
			i++
		}
		buf = binary.AppendUvarint(buf, uint64(zeros))
		buf = binary.AppendUvarint(buf, uint64(i-litStart))
		for _, v := range p.cells[litStart:i] {
			buf = binary.AppendUvarint(buf, uint64(v))
		}
	}
	return buf
}

// DeserializePage decodes a page produced by Serialize.
func DeserializePage(data []byte) (*SparsePage, error) {
	p := NewSparsePage()
	lsn, n := binary.Uvarint(data)
	if n <= 0 {
		return nil, fmt.Errorf("page: bad LSN encoding")
	}
	p.LSN = lsn
	data = data[n:]
	if len(data) == 0 {
		return nil, fmt.Errorf("page: missing mode byte")
	}
	mode := data[0]
	data = data[1:]
	switch mode {
	case 0:
		count, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("page: bad sparse count")
		}
		data = data[n:]
		prev := uint64(0)
		for i := 0; i < int(count); i++ {
			delta, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("page: bad sparse offset")
			}
			data = data[n:]
			val, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("page: bad sparse value")
			}
			data = data[n:]
			prev += delta
			if prev >= NumCells {
				return nil, fmt.Errorf("page: offset %d out of range", prev)
			}
			if val != 0 {
				p.sparse[uint32(prev)] = uint32(val)
				p.nonZero++
			}
		}
	case 1:
		p.dense = true
		p.cells = make([]uint32, NumCells)
		p.sparse = nil
		i := 0
		for i < NumCells {
			zeros, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("page: bad RLE zero run")
			}
			data = data[n:]
			litLen, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, fmt.Errorf("page: bad RLE literal count")
			}
			data = data[n:]
			i += int(zeros)
			for j := 0; j < int(litLen); j++ {
				if i >= NumCells {
					return nil, fmt.Errorf("page: RLE overflow")
				}
				v, n := binary.Uvarint(data)
				if n <= 0 {
					return nil, fmt.Errorf("page: bad RLE literal")
				}
				data = data[n:]
				p.cells[i] = uint32(v)
				if v != 0 {
					p.nonZero++
				}
				i++
			}
		}
	default:
		return nil, fmt.Errorf("page: unknown mode %d", mode)
	}
	return p, nil
}
