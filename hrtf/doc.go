// Package hrtf interpolates binaural head-related transfer functions over a
// spherical triangle mesh.
//
// A mesh vertex stores a left/right pair of equal-length FIR filters together
// with an explicit integer delay in samples. The stored filter is understood as
// the delay-removed (time-aligned) impulse response; the physical response is
// the filter shifted right by its integer delay.
//
// For a query direction the package:
//
//   - finds the spherical triangle containing the direction ray,
//   - intersects the ray with the triangle's chord plane and uses the
//     normalized planar barycentric coordinates as interpolation weights,
//   - interpolates the delay-removed FIRs separately for each ear,
//   - interpolates the integer delays and realizes the fractional part with
//     linear interpolation between the two adjacent samples.
//
// Both ears are evaluated for the same direction at the same sampling instant,
// keeping the interaural time difference coherent. Points on a shared edge are
// assigned to a fixed, deterministic triangle (the one with the smallest ID).
package hrtf
