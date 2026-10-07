package cutsolve

import (
	"fmt"
	"sort"
	"strings"
)

// Rect is a half-open pixel rectangle [X0,X1) x [Y0,Y1).
type Rect struct {
	X0, Y0, X1, Y1 int
}

func (r Rect) W() int    { return r.X1 - r.X0 }
func (r Rect) H() int    { return r.Y1 - r.Y0 }
func (r Rect) Area() int { return r.W() * r.H() }

func (r Rect) String() string {
	return fmt.Sprintf("(%d,%d)-(%d,%d)", r.X0, r.Y0, r.X1, r.Y1)
}

// Cut splits a rectangle along an integer line.
// Dir 'V': vertical line x=Pos (A=left, B=right).
// Dir 'H': horizontal line y=Pos (A=top, B=bottom).
type Cut struct {
	Dir  byte
	Pos  int
	A, B Rect
}

// Cuts lists every legal integer-line cut of r, verticals first then horizontals.
func (r Rect) Cuts() []Cut {
	var cs []Cut
	for x := r.X0 + 1; x < r.X1; x++ {
		cs = append(cs, Cut{'V', x, Rect{r.X0, r.Y0, x, r.Y1}, Rect{x, r.Y0, r.X1, r.Y1}})
	}
	for y := r.Y0 + 1; y < r.Y1; y++ {
		cs = append(cs, Cut{'H', y, Rect{r.X0, r.Y0, r.X1, y}, Rect{r.X0, y, r.X1, r.Y1}})
	}
	return cs
}

// allRects enumerates every rectangle inside a w x h grid, sorted by area
// ascending so that children always precede parents (dynamic-programming order).
func allRects(w, h int) []Rect {
	var rs []Rect
	for x0 := 0; x0 < w; x0++ {
		for x1 := x0 + 1; x1 <= w; x1++ {
			for y0 := 0; y0 < h; y0++ {
				for y1 := y0 + 1; y1 <= h; y1++ {
					rs = append(rs, Rect{x0, y0, x1, y1})
				}
			}
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Area() < rs[j].Area() })
	return rs
}

// Image is a binary picture with an integral image for O(1) region sums.
type Image struct {
	W, H int
	Pix  []byte // row-major, values 0/1
	ps   []int  // prefix sums, (H+1) x (W+1)
}

func NewImage(w, h int, pix []byte) Image {
	if len(pix) != w*h {
		panic("pixel count mismatch")
	}
	ps := make([]int, (h+1)*(w+1))
	for y := 0; y < h; y++ {
		row := 0
		for x := 0; x < w; x++ {
			row += int(pix[y*w+x])
			ps[(y+1)*(w+1)+x+1] = ps[y*(w+1)+x+1] + row
		}
	}
	return Image{W: w, H: h, Pix: pix, ps: ps}
}

// ParseImage parses rows of '0'/'1' separated by '/', ',', ';' or newlines.
func ParseImage(s string) (Image, error) {
	rows := strings.FieldsFunc(strings.TrimSpace(s), func(r rune) bool {
		return r == '/' || r == ',' || r == ';' || r == '\n'
	})
	if len(rows) == 0 {
		return Image{}, fmt.Errorf("empty image")
	}
	w := len(rows[0])
	if w == 0 || w > 8 || len(rows) > 8 {
		return Image{}, fmt.Errorf("image must be between 1x1 and 8x8, got %dx%d", w, len(rows))
	}
	pix := make([]byte, 0, w*len(rows))
	for _, row := range rows {
		if len(row) != w {
			return Image{}, fmt.Errorf("ragged rows: %q vs width %d", row, w)
		}
		for i := 0; i < len(row); i++ {
			switch row[i] {
			case '0':
				pix = append(pix, 0)
			case '1':
				pix = append(pix, 1)
			default:
				return Image{}, fmt.Errorf("invalid pixel %q in row %q", row[i], row)
			}
		}
	}
	return NewImage(w, len(rows), pix), nil
}

// Ones counts 1-pixels inside r.
func (im Image) Ones(r Rect) int {
	w := im.W + 1
	return im.ps[r.Y1*w+r.X1] - im.ps[r.Y0*w+r.X1] - im.ps[r.Y1*w+r.X0] + im.ps[r.Y0*w+r.X0]
}

// LeafStats returns the majority fill color of r (ties resolve to 0) and the
// number of miscolored pixels when r is filled with that color.
func (im Image) LeafStats(r Rect) (color, err int) {
	ones := im.Ones(r)
	zeros := r.Area() - ones
	if ones > zeros {
		return 1, zeros
	}
	return 0, ones
}
