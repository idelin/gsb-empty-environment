package meshsimp

import (
	"math"
)

// newUVSphere builds a closed latitude-longitude sphere of the given radius.
// segments is the number of longitude slices, rings the number of interior
// latitude bands (excluding the two poles).
func newUVSphere(radius float64, segments, rings int) *Mesh {
	m := &Mesh{}
	add := func(p [3]float64) int {
		m.Positions = append(m.Positions, p)
		return len(m.Positions) - 1
	}

	top := add([3]float64{0, radius, 0})
	ringStart := make([]int, rings+1)
	for r := 1; r <= rings; r++ {
		phi := math.Pi * float64(r) / float64(rings+1)
		y := radius * math.Cos(phi)
		ringR := radius * math.Sin(phi)
		ringStart[r] = len(m.Positions)
		for s := 0; s < segments; s++ {
			theta := 2 * math.Pi * float64(s) / float64(segments)
			add([3]float64{
				ringR * math.Cos(theta),
				y,
				ringR * math.Sin(theta),
			})
		}
	}
	bottom := add([3]float64{0, -radius, 0})

	idx := func(r, s int) int {
		return ringStart[r] + ((s%segments)+segments)%segments
	}

	// Top cap.
	for s := 0; s < segments; s++ {
		m.Faces = append(m.Faces, [3]int{top, idx(1, s), idx(1, s+1)})
	}
	// Middle bands.
	for r := 1; r < rings; r++ {
		for s := 0; s < segments; s++ {
			a, b := idx(r, s), idx(r, s+1)
			c, d := idx(r+1, s), idx(r+1, s+1)
			m.Faces = append(m.Faces,
				[3]int{a, c, b},
				[3]int{b, c, d},
			)
		}
	}
	// Bottom cap.
	for s := 0; s < segments; s++ {
		m.Faces = append(m.Faces, [3]int{bottom, idx(rings, s+1), idx(rings, s)})
	}

	m.Normals = make([][3]float64, len(m.Positions))
	m.UVs = make([][2]float64, len(m.Positions))
	m.Colors = make([][3]float64, len(m.Positions))
	for i, p := range m.Positions {
		n := normalize(p)
		m.Normals[i] = n
		m.UVs[i] = [2]float64{
			0.5 + math.Atan2(n[2], n[0])/(2*math.Pi),
			0.5 - math.Asin(n[1])/math.Pi,
		}
		m.Colors[i] = [3]float64{
			0.5 + n[0]/2, 0.5 + n[1]/2, 0.5 + n[2]/2,
		}
	}
	m.NormalizeAttributes = true
	return m
}

// newHoledPlane builds a square grid in the z=0 plane with a square hole in
// the middle. cellsPerSide must be even and at least 4. The result has two
// closed boundary loops (outer rim and inner hole), all wound counter
// clockwise when viewed from +z.
func newHoledPlane(cellsPerSide int) *Mesh {
	if cellsPerSide%2 != 0 || cellsPerSide < 4 {
		panic("cellsPerSide must be even and >= 4")
	}
	n := cellsPerSide
	half := cellsPerSide / 2

	vid := func(i, j int) int { return j*(n+1) + i }
	m := &Mesh{
		Positions: make([][3]float64, 0, (n+1)*(n+1)),
		UVs:       make([][2]float64, 0, (n+1)*(n+1)),
	}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			x := float64(i) / float64(n)
			y := float64(j) / float64(n)
			m.Positions = append(m.Positions, [3]float64{x, y, 0})
			m.UVs = append(m.UVs, [2]float64{x, y})
		}
	}

	insideHole := func(i, j int) bool {
		return i >= half/2 && i <= n-half/2 && j >= half/2 && j <= n-half/2
	}

	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			// Skip the four cells surrounding the central grid vertex that
			// form the square hole.
			if insideHole(i, j) || insideHole(i+1, j) ||
				insideHole(i, j+1) || insideHole(i+1, j+1) {
				continue
			}
			a, b := vid(i, j), vid(i+1, j)
			c, d := vid(i, j+1), vid(i+1, j+1)
			m.Faces = append(m.Faces,
				[3]int{a, b, c},
				[3]int{b, d, c},
			)
		}
	}
	return m
}

func boundaryEdgeCount(m *Mesh) int {
	count := make(map[uint64]int)
	for _, f := range m.Faces {
		for k := 0; k < 3; k++ {
			count[edgeKey(f[k], f[(k+1)%3])]++
		}
	}
	n := 0
	for _, c := range count {
		if c == 1 {
			n++
		}
	}
	return n
}

func boundaryVertexCount(m *Mesh) int {
	bv := make(map[int]struct{})
	count := make(map[uint64]int)
	for _, f := range m.Faces {
		for k := 0; k < 3; k++ {
			count[edgeKey(f[k], f[(k+1)%3])]++
		}
	}
	for key, c := range count {
		if c != 1 {
			continue
		}
		a, b := keyEnds(key)
		bv[a] = struct{}{}
		bv[b] = struct{}{}
	}
	return len(bv)
}
