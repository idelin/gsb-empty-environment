// Package laplacian implements Laplacian mesh deformation for triangle
// meshes.
//
// The editor preserves the per-vertex Laplacian coordinates of the rest pose
// using non-negative uniform adjacency weights (1/degree), minimizes the
// Laplacian coordinate residual and enforces control-point positions as hard
// constraints. Every connected component must contain at least one anchor;
// otherwise the reduced system is singular and NewEditor rejects the input.
//
// The normal-equation matrix is factorized once with a sparse simplicial
// LDLᵀ factorization. Updating only the anchor targets reuses the
// factorization, which keeps interactive dragging cheap.
//
// This package performs differential-coordinate geometry only; it is not a
// physical simulation.
package laplacian
