// Package tangent builds per-vertex tangent bases for asset pipelines that feed
// tangent-space normal maps into shaders.
//
// The generator splits mesh corners by position, normal smoothing group and UV
// orientation so that mirrored UV islands are never averaged across the mirror
// seam. Tangent and bitangent contributions are accumulated per split vertex
// with triangle-area weights, then re-orthogonalized against the (possibly
// smoothed) normal. Handedness is stored in its own channel rather than packed
// into the tangent, and corners with degenerate UVs receive a deterministic
// axis that is orthogonal to the normal.
package tangent

import (
	"errors"
	"math"
)

// Vec2 is a two-component UV coordinate.
type Vec2 struct {
	U, V float64
}

// Vec3 is a three component position, normal or direction vector.
type Vec3 struct {
	X, Y, Z float64
}

// Corner is one indexed triangle corner in the source mesh.
type Corner struct {
	Position Vec3
	Normal   Vec3
	UV       Vec2
	// Group is the normal smoothing group shared by connected smooth corners.
	// Corners with different groups are always split. Group zero means the
	// corner is faceted and never merges with any other corner.
	Group uint32
}

// Mat4 is a row-major 4x4 transform applied to an instance before basis
// generation, mirroring the transform applied when the instance is drawn.
type Mat4 [16]float64

// Identity returns the identity transform.
func Identity() Mat4 {
	return Mat4{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// Options controls optional generation behavior.
type Options struct {
	// Transform maps source geometry into instance space. Leave the pointer
	// nil to use identity, or pass Identity(). Negative-determinant transforms
	// (mirrored instances) are supported: normals use the inverse-transpose and
	// UV handedness is resolved in the transformed frame.
	Transform *Mat4
}

// Vertex is one split output vertex carrying an orthonormal tangent frame.
type Vertex struct {
	Position   Vec3
	Normal     Vec3    // unit length
	Tangent    Vec3    // unit length, orthogonal to Normal
	Handedness float64 // +1 or -1; stored separately from Tangent
	Source     int     // original corner index in the input corner list
}

// Result is the generated tangent basis plus the re-indexed triangle buffer.
type Result struct {
	Vertices []Vertex
	// Indices is the triangle list in terms of Vertices; len(Indices) equals
	// the number of input corners.
	Indices []int
}

var (
	errNoCorners    = errors.New("tangent: input has no corners")
	errCornerCount  = errors.New("tangent: corner count is not a multiple of three")
	errBadTransform = errors.New("tangent: transform must be non-singular")
)

const (
	uvEps   = 1e-12
	areaEps = 1e-12
	// Sentinel unique values for UV-valid corners; they never collide with
	// values minted by facetedUnique, which start at one.
	regularUnique uint64 = 1 << 63
	mirrorUnique  uint64 = 1<<63 - 1
)

type splitKey struct {
	px, py, pz float64
	u, v       float64
	group      uint32
	// unique keeps faceted corners from merging, and mirrored triangles from
	// merging across the mirror seam.
	unique uint64
}

type accumulator struct {
	position   Vec3
	normalSum  Vec3
	tangentSum Vec3
	bitanSum   Vec3
	handedness float64
	source     int
	uvValid    bool
}

// Generate splits corners, accumulates area-weighted tangent/bitangent
// contributions and returns the indexed tangent frame.
func Generate(corners []Corner, opts *Options) (*Result, error) {
	if len(corners) == 0 {
		return nil, errNoCorners
	}
	if len(corners)%3 != 0 {
		return nil, errCornerCount
	}

	xform := Identity()
	if opts != nil && opts.Transform != nil {
		xform = *opts.Transform
		if xform.determinant3() == 0 {
			return nil, errBadTransform
		}
	}
	normalMatrix := xform.inverseTranspose3()

	accums := make([]*accumulator, 0, len(corners))
	lookup := make(map[splitKey]int)
	outIndex := make([]int, len(corners))

	nextUnique := uint64(1)
	facetedUnique := func() uint64 {
		id := nextUnique
		nextUnique++
		return id
	}

	for tri := 0; tri < len(corners)/3; tri++ {
		ci := tri * 3
		a, b, c := corners[ci], corners[ci+1], corners[ci+2]

		pa := xform.transformPoint(a.Position)
		pb := xform.transformPoint(b.Position)
		pc := xform.transformPoint(c.Position)
		na := transformDir(normalMatrix, a.Normal)
		nb := transformDir(normalMatrix, b.Normal)
		nc := transformDir(normalMatrix, c.Normal)

		e1 := Sub(pb, pa)
		e2 := Sub(pc, pa)
		area := Cross(e1, e2).Length() * 0.5

		duv1 := Sub2(b.UV, a.UV)
		duv2 := Sub2(c.UV, a.UV)
		uvDet := duv1.U*duv2.V - duv2.U*duv1.V

		// Geometric face orientation combined with UV orientation gives a
		// frame-independent handedness, so mirrored instances stay correct.
		faceSign := 1.0
		if Dot(Cross(e1, e2), na) < 0 {
			faceSign = -1.0
		}

		var t, bv Vec3
		var handedness float64
		uvValid := math.Abs(uvDet) > uvEps
		if uvValid {
			inv := 1 / uvDet
			t = Scale(Add(
				Scale(e1, duv2.V),
				Scale(e2, -duv1.V),
			), inv)
			bv = Scale(Add(
				Scale(e1, -duv2.U),
				Scale(e2, duv1.U),
			), inv)
			if uvDet < 0 {
				handedness = -faceSign
			} else {
				handedness = faceSign
			}
		}

		// Degenerate triangles still need output vertices; the unique value is
		// reserved per degenerate triangle so they never merge with valid UVs.
		var degenerateUnique uint64
		if !uvValid {
			degenerateUnique = facetedUnique()
		}

		ps := [3]Vec3{pa, pb, pc}
		ns := [3]Vec3{na, nb, nc}
		cs := [3]Corner{a, b, c}
		for k := 0; k < 3; k++ {
			corner := cs[k]
			key := splitKey{
				px: ps[k].X, py: ps[k].Y, pz: ps[k].Z,
				u: corner.UV.U, v: corner.UV.V,
				group: corner.Group,
			}
			switch {
			case corner.Group == 0:
				key.unique = facetedUnique()
			case !uvValid:
				key.unique = degenerateUnique
			default:
				// Triangles with opposite UV orientation carry opposite
				// handedness; separating the key here is what prevents
				// averaging across a mirrored UV seam.
				if handedness < 0 {
					key.unique = mirrorUnique
				} else {
					key.unique = regularUnique
				}
			}

			idx, ok := lookup[key]
			if !ok {
				idx = len(accums)
				lookup[key] = idx
				accums = append(accums, &accumulator{
					position: ps[k],
					source:   ci + k,
				})
			}
			acc := accums[idx]
			acc.normalSum = Add(acc.normalSum, ns[k])
			if uvValid && area > areaEps {
				w := area
				acc.tangentSum = Add(acc.tangentSum, Scale(t, w))
				acc.bitanSum = Add(acc.bitanSum, Scale(bv, w))
				acc.handedness += handedness * w
				acc.uvValid = true
			}
			outIndex[ci+k] = idx
		}
	}

	verts := make([]Vertex, len(accums))
	for i, acc := range accums {
		normal, ok := Normalize(acc.normalSum)
		if !ok {
			normal = Vec3{Z: 1}
		}

		var tangent Vec3
		handedness := 1.0
		if acc.uvValid {
			// Gram-Schmidt against the smoothed normal.
			tn, ok := Normalize(Sub(
				acc.tangentSum,
				Scale(normal, Dot(acc.tangentSum, normal)),
			))
			if !ok {
				tn, ok = Normalize(Sub(
					acc.bitanSum,
					Scale(normal, Dot(acc.bitanSum, normal)),
				))
			}
			if !ok {
				tn = orthonormalAxis(normal)
			}
			tangent = tn
			if acc.handedness < 0 {
				handedness = -1.0
			}
		} else {
			// Degenerate UV: deterministic axis orthogonal to the normal.
			tangent = orthonormalAxis(normal)
		}

		verts[i] = Vertex{
			Position:   acc.position,
			Normal:     normal,
			Tangent:    tangent,
			Handedness: handedness,
			Source:     acc.source,
		}
	}

	return &Result{Vertices: verts, Indices: outIndex}, nil
}

// orthonormalAxis returns a deterministic unit vector orthogonal to n. The axis
// with the smallest absolute component is crossed with n, which is numerically
// stable for every normal direction.
func orthonormalAxis(n Vec3) Vec3 {
	ax := math.Abs(n.X)
	ay := math.Abs(n.Y)
	az := math.Abs(n.Z)
	var reference Vec3
	switch {
	case ax <= ay && ax <= az:
		reference = Vec3{X: 1}
	case ay <= az:
		reference = Vec3{Y: 1}
	default:
		reference = Vec3{Z: 1}
	}
	v, ok := Normalize(Cross(reference, n))
	if !ok {
		v, _ = Normalize(Cross(Vec3{Z: 1}, n))
	}
	return v
}

// Bitangent reconstructs the unit bitangent of v from its normal, tangent and
// the separately stored handedness.
func Bitangent(v Vertex) Vec3 {
	b := Cross(v.Normal, v.Tangent)
	if v.Handedness < 0 {
		b = Scale(b, -1)
	}
	return b
}

// PerturbedNormal transforms a tangent-space normal-map sample into the same
// space as Vertex.Normal. Sample components must already be decoded into signed
// components (typically in the [-1, 1] range).
func PerturbedNormal(v Vertex, sample Vec3) Vec3 {
	b := Bitangent(v)
	n := Add(
		Scale(v.Tangent, sample.X),
		Add(Scale(b, sample.Y), Scale(v.Normal, sample.Z)),
	)
	if out, ok := Normalize(n); ok {
		return out
	}
	return v.Normal
}

// Add returns a+b.
func Add(a, b Vec3) Vec3 { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }

// Sub returns a-b.
func Sub(a, b Vec3) Vec3 { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }

// Scale returns a*s.
func Scale(a Vec3, s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }

// Dot returns the scalar product a·b.
func Dot(a, b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

// Cross returns a×b.
func Cross(a, b Vec3) Vec3 {
	return Vec3{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

// Length returns |a|.
func (a Vec3) Length() float64 { return math.Sqrt(Dot(a, a)) }

// Normalize returns the unit vector and false when a is (near) zero length.
func Normalize(a Vec3) (Vec3, bool) {
	l := a.Length()
	if l <= areaEps {
		return Vec3{}, false
	}
	return Scale(a, 1/l), true
}

// Sub2 returns a-b.
func Sub2(a, b Vec2) Vec2 { return Vec2{a.U - b.U, a.V - b.V} }

func (m Mat4) at(row, col int) float64 { return m[row*4+col] }

func (m Mat4) transformPoint(p Vec3) Vec3 {
	return Vec3{
		X: m.at(0, 0)*p.X + m.at(0, 1)*p.Y + m.at(0, 2)*p.Z + m.at(0, 3),
		Y: m.at(1, 0)*p.X + m.at(1, 1)*p.Y + m.at(1, 2)*p.Z + m.at(1, 3),
		Z: m.at(2, 0)*p.X + m.at(2, 1)*p.Y + m.at(2, 2)*p.Z + m.at(2, 3),
	}
}

// upper3 returns the upper-left 3x3 as a row-major array of nine.
func (m Mat4) upper3() [9]float64 {
	return [9]float64{
		m.at(0, 0), m.at(0, 1), m.at(0, 2),
		m.at(1, 0), m.at(1, 1), m.at(1, 2),
		m.at(2, 0), m.at(2, 1), m.at(2, 2),
	}
}

func (m Mat4) determinant3() float64 {
	a := m.upper3()
	return a[0]*(a[4]*a[8]-a[5]*a[7]) -
		a[1]*(a[3]*a[8]-a[5]*a[6]) +
		a[2]*(a[3]*a[7]-a[4]*a[6])
}

// inverseTranspose3 returns the inverse-transpose of the upper 3x3, suitable
// for transforming normals.
func (m Mat4) inverseTranspose3() [9]float64 {
	a := m.upper3()
	inv := 1 / m.determinant3()
	// Transposed cofactor matrix; result row i / col j holds cofactor(a, i, j).
	return [9]float64{
		(a[4]*a[8] - a[5]*a[7]) * inv,
		(a[5]*a[6] - a[3]*a[8]) * inv,
		(a[3]*a[7] - a[4]*a[6]) * inv,

		(a[2]*a[7] - a[1]*a[8]) * inv,
		(a[0]*a[8] - a[2]*a[6]) * inv,
		(a[1]*a[6] - a[0]*a[7]) * inv,

		(a[1]*a[5] - a[2]*a[4]) * inv,
		(a[2]*a[3] - a[0]*a[5]) * inv,
		(a[0]*a[4] - a[1]*a[3]) * inv,
	}
}

func transformDir(m [9]float64, d Vec3) Vec3 {
	return Vec3{
		X: m[0]*d.X + m[1]*d.Y + m[2]*d.Z,
		Y: m[3]*d.X + m[4]*d.Y + m[5]*d.Z,
		Z: m[6]*d.X + m[7]*d.Y + m[8]*d.Z,
	}
}
