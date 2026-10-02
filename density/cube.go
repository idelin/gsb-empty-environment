package density

import "time"

const (
	coordBits  = 20
	coordBias  = 1 << (coordBits - 1)
	coordMask  = 1<<coordBits - 1
	hourShift  = 2 * coordBits
	maxHourKey = 1 << (64 - hourShift) // 24 bits of hour index
)

// Cube is a streaming spatio-temporal data cube: weights are accumulated per
// (hour, tileX, tileY) cell in a flat hash map keyed by a packed uint64, so
// memory stays proportional to the number of *distinct active cells*, not to
// the number of ingested points.
type Cube struct {
	grid  *Grid
	cells map[uint64]float64
}

// NewCube creates an empty cube on the given grid.
func NewCube(g *Grid) *Cube {
	return &Cube{grid: g, cells: make(map[uint64]float64)}
}

// pack combines an hour index and biased tile coordinates into one key.
// Tile coordinates must fall within ±coordBias tiles of the origin
// (±524288 tiles); hours since epoch must fit in 24 bits (until year ~3880).
func pack(hour int64, x, y int32) (uint64, bool) {
	if hour < 0 || hour >= maxHourKey {
		return 0, false
	}
	bx := int64(x) + coordBias
	by := int64(y) + coordBias
	if bx < 0 || bx > coordMask || by < 0 || by > coordMask {
		return 0, false
	}
	return uint64(hour)<<hourShift | uint64(bx)<<coordBits | uint64(by), true
}

func unpack(key uint64) (hour int64, x, y int32) {
	hour = int64(key >> hourShift)
	x = int32(int64(key>>coordBits&coordMask) - coordBias)
	y = int32(int64(key&coordMask) - coordBias)
	return hour, x, y
}

// hourOf returns the hourly bucket index for a timestamp.
func hourOf(t time.Time) int64 { return t.Unix() / 3600 }

// Add accumulates weight w for the trajectory point (lat, lng) observed at
// time t. It is O(1). It returns false if the point falls outside the
// representable grid/time range (and is then dropped).
func (c *Cube) Add(lat, lng float64, t time.Time, w float64) bool {
	x, y := c.grid.Locate(lat, lng)
	key, ok := pack(hourOf(t), x, y)
	if !ok {
		return false
	}
	c.cells[key] += w
	return true
}

// Len returns the number of distinct active (hour, tile) cells, which drives
// memory usage.
func (c *Cube) Len() int { return len(c.cells) }

// Prune drops all cells whose hourly bucket ends at or before `before`,
// bounding memory when the retention window is known. It returns the number
// of cells removed.
func (c *Cube) Prune(before time.Time) int {
	threshold := hourOf(before)
	removed := 0
	for key := range c.cells {
		if hour, _, _ := unpack(key); hour < threshold {
			delete(c.cells, key)
			removed++
		}
	}
	return removed
}

// Weight returns the accumulated weight of a single (hour, tile) cell.
func (c *Cube) Weight(t time.Time, x, y int32) float64 {
	key, ok := pack(hourOf(t), x, y)
	if !ok {
		return 0
	}
	return c.cells[key]
}
