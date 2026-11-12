// Package taskpkg cuts an oriented closed triangle mesh by a rational plane
// and builds a rendered cap for the retained half-space.
//
// Cut retains the side A*x+B*y+C*z+D >= 0. Input must be a closed, consistently
// oriented triangular 2-manifold. Intersection vertices are shared by original
// edge identity, exposed cap boundaries are assembled into oriented loops, and
// loops with holes are triangulated in plane coordinates. Coplanar faces are
// classified by normal and are not capped twice.
package taskpkg
