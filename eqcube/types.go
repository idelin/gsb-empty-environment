// Package eqcube resamples an equirectangular (等距柱状) panorama into
// cubemap faces.
//
// Every cubemap output pixel is mapped, through the explicit face basis
// (right / up / forward columns of a rotation matrix), to a unit direction.
// The equirectangular source is sampled with periodic longitude wrapping so
// that lines crossing the +/-180 degree seam stay connected, and with
// latitude clamping at the poles. The filter places an adaptive grid of
// sub-samples in direction space so that the full solid angle of every output
// pixel is covered; sub-sample weights are normalized by cubemap-pixel solid
// angle.
package eqcube

import (
	"image"
	"math"
)

// Vec3 is a direction in world space.
type Vec3 struct {
	X, Y, Z float64
}

// Normalize returns the unit vector pointing in the same direction as v.
// The zero vector maps to (0, 0, 0).
func (v Vec3) Normalize() Vec3 {
	norm := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if norm == 0 {
		return Vec3{}
	}
	return Vec3{X: v.X / norm, Y: v.Y / norm, Z: v.Z / norm}
}

// Mat3 is a 3x3 orientation matrix stored column by column.
//
// For a cubemap face the columns are, in order:
//
//	Right — world direction of local face coordinate +u (texture right);
//	Up    — world direction of local face coordinate +v (texture up);
//	Front — world direction of the face center (local direction (0,0,1)).
//
// A local face vector (u, v, 1) is turned into a world direction with
// R.Apply, i.e. world = Right*u + Up*v + Front. The three columns must be
// orthonormal and right handed; DefaultFaces returns the standard set.
type Mat3 struct {
	// Columns C0 (right), C1 (up), C2 (front).
	C0, C1, C2 Vec3
}

// Apply returns m*x for column vector x.
func (m Mat3) Apply(x Vec3) Vec3 {
	return Vec3{
		X: m.C0.X*x.X + m.C1.X*x.Y + m.C2.X*x.Z,
		Y: m.C0.Y*x.X + m.C1.Y*x.Y + m.C2.Y*x.Z,
		Z: m.C0.Z*x.X + m.C1.Z*x.Y + m.C2.Z*x.Z,
	}
}

// Face describes one cubemap face: its orientation and the rendered image.
type Face struct {
	// Name is an optional label, e.g. "+X", used only for diagnostics.
	Name string
	// Rotation maps local face coordinates to world directions.
	Rotation Mat3
	// Image receives the resampled result; its bounds define face size.
	Image *image.NRGBA
}

// DefaultFaces allocates the six standard faces of an edge-length-size
// cubemap in the world frame X=east, Y=north/up, Z=front:
//
//	+Z front, -Z back, +X right, -X left, +Y top, -Y bottom.
//
// Longitude is measured around the Y axis and latitude off the X-Z plane,
// matching the Equirect convention.
func DefaultFaces(size int) []Face {
	return []Face{
		{Name: "+X", Rotation: Mat3{
			C0: Vec3{0, 0, -1}, C1: Vec3{0, 1, 0}, C2: Vec3{1, 0, 0},
		}},
		{Name: "-X", Rotation: Mat3{
			C0: Vec3{0, 0, 1}, C1: Vec3{0, 1, 0}, C2: Vec3{-1, 0, 0},
		}},
		{Name: "+Y", Rotation: Mat3{
			C0: Vec3{1, 0, 0}, C1: Vec3{0, 0, -1}, C2: Vec3{0, 1, 0},
		}},
		{Name: "-Y", Rotation: Mat3{
			C0: Vec3{1, 0, 0}, C1: Vec3{0, 0, 1}, C2: Vec3{0, -1, 0},
		}},
		{Name: "+Z", Rotation: Mat3{
			C0: Vec3{1, 0, 0}, C1: Vec3{0, 1, 0}, C2: Vec3{0, 0, 1},
		}},
		{Name: "-Z", Rotation: Mat3{
			C0: Vec3{-1, 0, 0}, C1: Vec3{0, 1, 0}, C2: Vec3{0, 0, -1},
		}},
	}
}
