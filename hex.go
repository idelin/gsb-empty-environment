package hexgrid

import "fmt"

// Hex is a cell in axial coordinates (Q, R) on a flat-topped hex grid.
// No geographic projection is involved; this is a pure 2D grid.
type Hex struct {
	Q, R int
}

// Directions are the six fixed neighbor offsets of a cell, in axial
// coordinates. The order is part of the specification and must not change.
var Directions = [6]Hex{
	{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// Add returns the cell h+d.
func (h Hex) Add(d Hex) Hex { return Hex{h.Q + d.Q, h.R + d.R} }

// Neighbors returns the six adjacent cells in Directions order.
func (h Hex) Neighbors() [6]Hex {
	var out [6]Hex
	for i, d := range Directions {
		out[i] = h.Add(d)
	}
	return out
}

// Less orders cells lexicographically by (Q, R). It is the tie-break used
// to pick a single deterministic path among equally short ones.
func (h Hex) Less(o Hex) bool {
	return h.Q < o.Q || (h.Q == o.Q && h.R < o.R)
}

func (h Hex) String() string { return fmt.Sprintf("(%d,%d)", h.Q, h.R) }
