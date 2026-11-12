// Package taskpkg plans padded, mip-safe texture atlases for an asset
// pipeline.
//
// Inputs are placed into a power-of-two atlas. The planner minimizes atlas
// area first and the length of its longer side second. Ties are resolved
// deterministically by the per-ID position vectors.
//
// Every placement is rounded up at mip level zero to a multiple of the
// coarsest mip scale. That aligned block is surrounded by a one-pixel border
// at every mip level, so the base-level border is 2^maxMipLevel pixels rather
// than a hard-coded two pixels.
package taskpkg
