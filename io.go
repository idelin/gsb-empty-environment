package dtri

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// BigInt is a JSON-marshalable big integer. It accepts both a JSON number
// (of arbitrary size, without float64 rounding) and a JSON string containing
// a decimal integer.
type BigInt struct {
	*big.Int
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *BigInt) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		b.Int = nil
		return nil
	}
	if strings.HasPrefix(s, "\"") {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = str
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return fmt.Errorf("dtri: invalid integer %q", s)
	}
	b.Int = v
	return nil
}

// MarshalJSON implements json.Marshaler. The value is emitted as a JSON
// number (exact integer literal).
func (b BigInt) MarshalJSON() ([]byte, error) {
	if b.Int == nil {
		return []byte("0"), nil
	}
	return []byte(b.Int.String()), nil
}

// InputJSON is the JSON input document:
//
//	{"points": [{"id": "A-1", "x": 12, "y": "-7"}, ...]}
//
// "id" is optional (string or number); points without an id get their input
// index as id. "x" and "y" are integers of arbitrary size, as JSON numbers
// or strings.
type InputJSON struct {
	Points []InputPointJSON `json:"points"`
}

// InputPointJSON is one survey point in the JSON input.
type InputPointJSON struct {
	ID string `json:"id"`
	X  BigInt `json:"x"`
	Y  BigInt `json:"y"`
}

// UnmarshalJSON implements json.Unmarshaler, accepting a string or number id.
func (p *InputPointJSON) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID json.RawMessage `json:"id"`
		X  BigInt          `json:"x"`
		Y  BigInt          `json:"y"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.X, p.Y = raw.X, raw.Y
	if len(raw.ID) > 0 {
		s := strings.TrimSpace(string(raw.ID))
		if strings.HasPrefix(s, "\"") {
			if err := json.Unmarshal(raw.ID, &s); err != nil {
				return err
			}
		}
		p.ID = s
	}
	return nil
}

// DecodeInput parses a JSON input document.
func DecodeInput(data []byte) ([]InputPoint, error) {
	var in InputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	pts := make([]InputPoint, 0, len(in.Points))
	for i, p := range in.Points {
		if p.X.Int == nil || p.Y.Int == nil {
			return nil, fmt.Errorf("dtri: point %d missing x or y", i)
		}
		pts = append(pts, InputPoint{ID: p.ID, X: p.X.Int, Y: p.Y.Int})
	}
	return pts, nil
}

// OutputJSON is the JSON output document.
type OutputJSON struct {
	Status    string         `json:"status"`
	Points    []PointJSON    `json:"points"`
	Triangles []TriangleJSON `json:"triangles"`
	Hull      []int          `json:"hull"`
	Chain     []int          `json:"chain,omitempty"`
}

// PointJSON is one distinct output point with its merged source IDs.
type PointJSON struct {
	Index int      `json:"index"`
	X     BigInt   `json:"x"`
	Y     BigInt   `json:"y"`
	IDs   []string `json:"ids"`
}

// TriangleJSON is one CCW triangle with its neighbor table.
// Neighbors[k] is the triangle across the edge opposite Vertices[k], or -1.
type TriangleJSON struct {
	Vertices  [3]int `json:"vertices"`
	Neighbors [3]int `json:"neighbors"`
}

// EncodeOutput renders an Output as the canonical JSON document.
func EncodeOutput(out *Output) ([]byte, error) {
	doc := OutputJSON{
		Status:    string(out.Status),
		Points:    make([]PointJSON, len(out.Points)),
		Triangles: make([]TriangleJSON, len(out.Triangles)),
		Hull:      out.Hull,
		Chain:     out.Chain,
	}
	if doc.Hull == nil {
		doc.Hull = []int{}
	}
	for i, p := range out.Points {
		doc.Points[i] = PointJSON{Index: i, X: BigInt{p.X}, Y: BigInt{p.Y}, IDs: out.IDs[i]}
	}
	for i := range out.Triangles {
		doc.Triangles[i] = TriangleJSON{Vertices: out.Triangles[i], Neighbors: out.Neighbors[i]}
	}
	return json.MarshalIndent(doc, "", "  ")
}
