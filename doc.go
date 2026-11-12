// Package taskpkg implements exact two-dimensional Boolean regions for a
// vector pipeline.
//
// Input rings are closed point sequences; the final point connects back to
// the first point. Coordinates use math/big.Rat throughout. Both nonzero and
// even-odd fill rules are supported.
//
// Boolean results are canonical boundaries with counter-clockwise outer
// contours and clockwise holes. Every output contour edge retains the source
// operand, ring, and original segment index. Intersections and collinear
// overlap endpoints are split exactly; no floating-point tolerance is used.
package taskpkg
