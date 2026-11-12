package laplacian

import "errors"

// Vec3 is a three-dimensional position.
type Vec3 struct {
	X, Y, Z float64
}

// Mesh is a triangle mesh described by vertex positions and indexed faces.
// Faces reference vertices by index; the mesh may consist of several
// disconnected components.
type Mesh struct {
	Vertices []Vec3
	Faces    [][3]int
}

// Result holds the deformed vertex positions and quality statistics.
type Result struct {
	// Positions contains the deformed position of every vertex.
	Positions []Vec3

	// AnchorMax is the largest absolute distance between an anchor result and
	// its requested target. Hard constraints make this (numerically) zero.
	AnchorMax float64

	// VertexResidual is the Laplacian-coordinate residual norm per vertex.
	VertexResidual []float64

	// RMSResidual is the root-mean-square Laplacian residual over all vertices.
	RMSResidual float64

	// MaxResidual is the largest per-vertex Laplacian residual norm.
	MaxResidual float64

	// ReusedFactorization reports whether a precomputed LDLᵀ factorization was
	// reused instead of rebuilding the reduced system.
	ReusedFactorization bool
}

var (
	// ErrInvalidMesh is returned for empty meshes, bad indices or non-finite
	// vertex coordinates.
	ErrInvalidMesh = errors.New("laplacian: invalid mesh")

	// ErrNoAnchors is returned when no control points are supplied.
	ErrNoAnchors = errors.New("laplacian: no anchor vertices")

	// ErrAnchorOutOfRange is returned when an anchor index is not a valid
	// vertex index or an anchor list has no matching target.
	ErrAnchorOutOfRange = errors.New("laplacian: anchor index out of range")

	// ErrComponentWithoutAnchor is returned when a connected component has no
	// anchor, which would leave an unconstrained (singular) system.
	ErrComponentWithoutAnchor = errors.New("laplacian: connected component without anchor")
)
