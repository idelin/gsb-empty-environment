package taskpkg

import "sync/atomic"

// Frame holds the vertex positions of a mesh at the two ends of a motion
// interval. Positions in Pos0 are the vertex locations at time 0, Pos1 at
// time 1; vertices move linearly between them.
type Frame struct {
	Pos0 []Vec3
	Pos1 []Vec3
}

// Mesh is a triangle mesh whose vertices move linearly between the two
// position sets of the current Frame. The triangle topology is fixed; only
// vertex positions change between frames.
//
// The current frame is swapped atomically, so queries in flight always see
// one consistent set of positions even while the host installs the next
// frame with SetFrame.
type Mesh struct {
	Tris  [][3]int32
	frame atomic.Pointer[Frame]
}

// NewMesh builds a mesh from triangle indices and the vertex positions at
// both ends of the motion interval. pos0 and pos1 must have equal length
// and must not be mutated after the call.
func NewMesh(tris [][3]int32, pos0, pos1 []Vec3) *Mesh {
	m := &Mesh{Tris: tris}
	m.frame.Store(&Frame{Pos0: pos0, Pos1: pos1})
	return m
}

// SetFrame installs a new motion interval. The slices must not be mutated
// after the call; concurrent queries keep seeing the previous frame until
// the swap.
func (m *Mesh) SetFrame(pos0, pos1 []Vec3) {
	m.frame.Store(&Frame{Pos0: pos0, Pos1: pos1})
}

// Frame returns the currently installed motion interval.
func (m *Mesh) Frame() *Frame { return m.frame.Load() }

// Vertex returns the position of corner k (0..2) of triangle tri,
// linearly interpolated between the frame endpoints at the given time.
func (m *Mesh) Vertex(f *Frame, tri, k int, time float64) Vec3 {
	idx := m.Tris[tri][k]
	return f.Pos0[idx].Lerp(f.Pos1[idx], time)
}

// TriBounds returns the swept bounds of a triangle: the box covering its
// vertices at both ends of the motion interval, so any linearly
// interpolated position is contained.
func (m *Mesh) TriBounds(f *Frame, tri int) AABB {
	box := emptyAABB()
	for k := 0; k < 3; k++ {
		idx := m.Tris[tri][k]
		box = box.Extend(f.Pos0[idx])
		box = box.Extend(f.Pos1[idx])
	}
	return box
}
