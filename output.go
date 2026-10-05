package delaunay

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Output modes.
const (
	ModeEmpty         = "empty"         // no points
	ModePoint         = "point"         // a single distinct point
	ModeSegment       = "segment"       // two distinct points
	ModeCollinear     = "collinear"     // >= 3 points, all collinear
	ModeTriangulation = "triangulation" // >= 3 points, not all collinear
)

// OutputPoint is a distinct input point with its source IDs.
type OutputPoint struct {
	Index int         `json:"index"`
	X     json.Number `json:"x"`
	Y     json.Number `json:"y"`
	IDs   []string    `json:"ids"`
}

// Output is the canonical triangulation result.
//
// Triangles are counterclockwise, each rotated to start at its smallest
// vertex index, and sorted lexicographically. Adjacency[t][i] is the index
// of the triangle sharing the edge opposite vertex i of triangle t, or -1
// for a boundary edge. Hull lists boundary vertex indices in CCW order
// (collinear boundary points included). Chain is the ordered point list for
// the degenerate modes (point, segment, collinear).
type Output struct {
	Mode      string        `json:"mode"`
	Points    []OutputPoint `json:"points"`
	Triangles [][3]int      `json:"triangles,omitempty"`
	Adjacency [][3]int      `json:"adjacency,omitempty"`
	Hull      []int         `json:"hull,omitempty"`
	Chain     []int         `json:"chain,omitempty"`
}

func jsonNumber(v *big.Int) json.Number { return json.Number(v.String()) }

// InputPoint is one raw input point with arbitrary-precision integer
// coordinates (JSON numbers or strings both decode into json.Number).
type InputPoint struct {
	ID string      `json:"id"`
	X  json.Number `json:"x"`
	Y  json.Number `json:"y"`
}

// Input is the JSON input document: {"points":[{"id":"p0","x":0,"y":0},...]}.
type Input struct {
	Points []InputPoint `json:"points"`
}

// RawPoint is a parsed input point.
type RawPoint struct {
	ID   string
	X, Y *big.Int
}

// Raw converts the input to exact integer points.
func (in *Input) Raw() ([]RawPoint, error) {
	raw := make([]RawPoint, 0, len(in.Points))
	for i, p := range in.Points {
		x, ok := new(big.Int).SetString(string(p.X), 10)
		if !ok {
			return nil, fmt.Errorf("point %d (id %q): invalid integer x %q", i, p.ID, string(p.X))
		}
		y, ok := new(big.Int).SetString(string(p.Y), 10)
		if !ok {
			return nil, fmt.Errorf("point %d (id %q): invalid integer y %q", i, p.ID, string(p.Y))
		}
		id := p.ID
		if id == "" {
			id = fmt.Sprintf("p%d", i)
		}
		raw = append(raw, RawPoint{ID: id, X: x, Y: y})
	}
	return raw, nil
}

// Triangulate builds the canonical triangulation for raw input points.
func Triangulate(raw []RawPoint) *Output {
	t := New()
	for _, p := range raw {
		t.Insert(p.X, p.Y, p.ID)
	}
	return t.Result()
}

// pointByIndex returns the output point with the given index.
func (o *Output) pointByIndex(i int) (Point, error) {
	if i < 0 || i >= len(o.Points) {
		return Point{}, fmt.Errorf("point index %d out of range [0,%d)", i, len(o.Points))
	}
	op := o.Points[i]
	x, ok := new(big.Int).SetString(string(op.X), 10)
	if !ok {
		return Point{}, fmt.Errorf("point %d: invalid x", i)
	}
	y, ok := new(big.Int).SetString(string(op.Y), 10)
	if !ok {
		return Point{}, fmt.Errorf("point %d: invalid y", i)
	}
	return Point{x, y}, nil
}

// GeomPoints decodes the output points as exact Points indexed by Index.
func (o *Output) GeomPoints() ([]Point, error) {
	pts := make([]Point, len(o.Points))
	for i := range o.Points {
		p, err := o.pointByIndex(i)
		if err != nil {
			return nil, err
		}
		if o.Points[i].Index != i {
			return nil, fmt.Errorf("points[%d].index = %d, want %d", i, o.Points[i].Index, i)
		}
		pts[i] = p
	}
	return pts, nil
}
