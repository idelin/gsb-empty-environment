package hrtf

import (
	"errors"
	"math"
)

// Geometric tolerances.
const (
	// epsParallel rejects rays parallel to a triangle's chord plane.
	epsParallel = 1e-12
	// epsContain decides whether a barycentric coordinate counts as inside a
	// triangle. Slightly negative values are clamped before normalization.
	epsContain = 1e-9
)

// Vec3 is a unit direction or a point on the unit sphere.
type Vec3 struct {
	X, Y, Z float64
}

func (v Vec3) add(o Vec3) Vec3      { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) sub(o Vec3) Vec3      { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) scale(s float64) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }
func (v Vec3) dot(o Vec3) float64   { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }

func (v Vec3) cross(o Vec3) Vec3 {
	return Vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}

// norm returns the Euclidean length of the vector.
func (v Vec3) norm() float64 { return math.Sqrt(v.dot(v)) }

// normalize scales the vector to unit length. The zero vector is rejected by
// the mesh constructor.
func (v Vec3) normalize() Vec3 {
	n := v.norm()
	return v.scale(1 / n)
}

// Channel identifies an ear.
type Channel int

const (
	// Left is the left ear channel.
	Left Channel = iota
	// Right is the right ear channel.
	Right
)

// Filter holds one ear's delay-removed FIR and its explicit integer delay.
// The physical impulse response equals FIR shifted right by Delay samples.
type Filter struct {
	// FIR contains the delay-removed coefficients.
	FIR []float64
	// Delay is the explicit integer delay in samples, Delay >= 0.
	Delay int
}

// Vertex is one node of the spherical mesh with its binaural measurement.
type Vertex struct {
	// Dir is the (not necessarily unit) measurement direction.
	Dir Vec3
	// Left and Right hold equal-length delay-removed FIRs.
	Left, Right Filter
}

// Face is a triangle referencing three vertices by index.
type Face struct {
	// ID is the stable identifier used for deterministic boundary selection.
	// It must be unique across the mesh.
	ID int
	// A, B and C are vertex indices.
	A, B, C int
}

// Mesh is an immutable spherical triangle mesh of binaural measurements.
type Mesh struct {
	vertices []Vertex
	faces    []Face
	// unit[i] is the normalized direction of vertices[i].
	unit []Vec3
	// length is the common FIR length shared by every channel.
	length int
}

// NewMesh validates and builds a mesh. All FIRs across every vertex and both
// ears must be non-empty and share one length; every delay must be non-negative
// and every face must reference three distinct, existing vertices.
func NewMesh(vertices []Vertex, faces []Face) (*Mesh, error) {
	if len(vertices) == 0 {
		return nil, errors.New("hrtf: mesh requires at least one vertex")
	}
	if len(faces) == 0 {
		return nil, errors.New("hrtf: mesh requires at least one face")
	}

	unit := make([]Vec3, len(vertices))
	length := 0
	for i := range vertices {
		v := &vertices[i]
		if v.Dir.norm() == 0 {
			return nil, errors.New("hrtf: vertex direction must not be zero")
		}
		unit[i] = v.Dir.normalize()
		if len(v.Left.FIR) == 0 || len(v.Right.FIR) == 0 {
			return nil, errors.New("hrtf: every channel requires a non-empty FIR")
		}
		if len(v.Left.FIR) != len(v.Right.FIR) {
			return nil, errors.New("hrtf: left and right FIRs must have equal length")
		}
		if v.Left.Delay < 0 || v.Right.Delay < 0 {
			return nil, errors.New("hrtf: delays must be non-negative")
		}
		if i == 0 {
			length = len(v.Left.FIR)
		} else if len(v.Left.FIR) != length {
			return nil, errors.New("hrtf: all FIRs must share one length")
		}
	}

	seenIDs := make(map[int]bool, len(faces))
	for i := range faces {
		f := &faces[i]
		if seenIDs[f.ID] {
			return nil, errors.New("hrtf: duplicate face ID")
		}
		seenIDs[f.ID] = true
		if f.A < 0 || f.B < 0 || f.C < 0 ||
			f.A >= len(vertices) || f.B >= len(vertices) || f.C >= len(vertices) {
			return nil, errors.New("hrtf: face vertex index out of range")
		}
		if f.A == f.B || f.B == f.C || f.C == f.A {
			return nil, errors.New("hrtf: face requires three distinct vertices")
		}
	}

	// Defensive copies keep the mesh immutable after construction.
	vs := append([]Vertex(nil), vertices...)
	for i := range vs {
		vs[i].Left.FIR = append([]float64(nil), vertices[i].Left.FIR...)
		vs[i].Right.FIR = append([]float64(nil), vertices[i].Right.FIR...)
	}
	fs := append([]Face(nil), faces...)
	return &Mesh{vertices: vs, faces: fs, unit: unit, length: length}, nil
}

// FIRLength returns the common FIR length of every channel in the mesh.
func (m *Mesh) FIRLength() int { return m.length }

// VertexCount returns the number of mesh vertices.
func (m *Mesh) VertexCount() int { return len(m.vertices) }

// FaceCount returns the number of mesh faces.
func (m *Mesh) FaceCount() int { return len(m.faces) }

// Location describes where a direction ray falls on the mesh.
type Location struct {
	// Face is the selected triangle.
	Face Face
	// A, B and C are the normalized barycentric weights of the intersection
	// of the ray with the triangle's chord plane. They sum to one.
	A, B, C float64
	// Vertices are the three referenced vertex indices.
	Vertices [3]int
}

// Weights returns the barycentric triple as a slice in face-vertex order.
func (l Location) Weights() []float64 { return []float64{l.A, l.B, l.C} }

// Locate finds the spherical triangle containing dir. dir may be unnormalized.
// The ray starting at the origin is intersected with each face's chord plane;
// the planar barycentric coordinates of that intersection give the weights.
//
// For a ray lying on a shared edge (or through a vertex), several faces match.
// The face with the smallest ID is selected deterministically, so repeated or
// simultaneous left/right queries always resolve to the same triangle.
func (m *Mesh) Locate(dir Vec3) (Location, error) {
	if dir.norm() == 0 {
		return Location{}, errors.New("hrtf: query direction must not be zero")
	}
	r := dir.normalize()

	best := Location{}
	found := false
	for i := range m.faces {
		face := &m.faces[i]
		p0 := m.unit[face.A]
		e1 := m.unit[face.B].sub(p0)
		e2 := m.unit[face.C].sub(p0)

		// Moller-Trumbore intersection for a ray O + t*r with O at the origin.
		// tvec = O - p0 = -p0.
		pvec := r.cross(e2)
		det := e1.dot(pvec)
		if math.Abs(det) < epsParallel {
			continue
		}
		invDet := 1 / det
		tvec := p0.scale(-1)
		wB := tvec.dot(pvec) * invDet // weight along e1
		qvec := tvec.cross(e1)
		wC := r.dot(qvec) * invDet // weight along e2
		t := e2.dot(qvec) * invDet
		if t <= 0 {
			continue // intersection behind the ray origin
		}
		wA := 1 - wB - wC

		if wA < -epsContain || wB < -epsContain || wC < -epsContain {
			continue
		}
		wA = math.Max(wA, 0)
		wB = math.Max(wB, 0)
		wC = math.Max(wC, 0)
		sum := wA + wB + wC
		wA /= sum
		wB /= sum
		wC /= sum

		if !found || face.ID < best.Face.ID {
			found = true
			best = Location{
				Face:     *face,
				A:        wA,
				B:        wB,
				C:        wC,
				Vertices: [3]int{face.A, face.B, face.C},
			}
		}
	}

	if !found {
		return Location{}, errors.New("hrtf: direction does not intersect any mesh face")
	}
	return best, nil
}
