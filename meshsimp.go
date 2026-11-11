// Package meshsimp simplifies triangle meshes using quadric error metric
// (QEM) edge collapses as described by Garland and Heckbert.
//
// The simplifier maintains every candidate edge in a minimum heap keyed by
// its QEM collapse cost. Collapses elsewhere invalidate candidates, so each
// heap entry carries a version tag: stale entries are detected and discarded
// when they reach the top of the heap.
//
// A collapse is rejected when it would flip a face normal, produce a
// non-manifold edge, or modify an open boundary. Boundary vertices are
// therefore frozen and the boundary geometry is preserved exactly.
package meshsimp
