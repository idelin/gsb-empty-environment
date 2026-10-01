package cidx

import (
	"math"
	"sort"
)

// Guarded is the only read path a Mapping exposes. It refuses any read that
// is not fully contained in a region the checker validated, so the query
// path can never observe unvalidated bytes.
type Guarded struct {
	data    []byte
	regions []Region // merged, sorted by Start
}

// NewGuarded wraps data with the validated regions of lay.
func NewGuarded(data []byte, lay *Layout) *Guarded {
	return &Guarded{data: data, regions: lay.Regions}
}

// ReadAt returns the bytes in [off, off+n). It fails unless the whole range
// lies inside a single validated region.
func (g *Guarded) ReadAt(off, n uint64) ([]byte, error) {
	if off > math.MaxUint64-n {
		return nil, ErrOverflow
	}
	end := off + n
	if end > uint64(len(g.data)) {
		return nil, ErrOutOfBounds
	}
	if !g.covered(off, end) {
		return nil, ErrUnvalidatedRead
	}
	return g.data[off:end], nil
}

func (g *Guarded) covered(start, end uint64) bool {
	i := sort.Search(len(g.regions), func(i int) bool {
		return g.regions[i].Start > start
	}) - 1
	if i < 0 {
		return false
	}
	return g.regions[i].End >= end
}
