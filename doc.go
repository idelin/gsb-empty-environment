// Package taskpkg implements harmonic (Tutte) parameterization for the asset
// pipeline.
//
// Parameterize accepts an oriented triangle mesh whose topology is a disk
// (connected, manifold, exactly one boundary loop, Euler characteristic one).
// The boundary loop is mapped onto the unit circle by cumulative edge length;
// every interior vertex is constrained to the arithmetic mean of its neighbors.
// That mean-value condition yields the symmetric positive definite sparse
// system deg(i)*u_i - sum u_j = boundary contribution, solved with a Jacobi
// preconditioned conjugate gradient method.
//
// The result reports every vertex UV, the ordered boundary correspondence,
// the signed UV area of each face and the maximum solver residual. Inputs
// with multiple boundary loops or non-manifold topology are rejected. A
// flipped or degenerate face is never accepted silently: Parameterize returns
// a *FlipError carrying evidence of the offending face.
package taskpkg
