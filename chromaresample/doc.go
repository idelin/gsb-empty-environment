// Package chromaresample relocates planar YUV chroma (or luma) samples
// between sampling grids with explicitly declared sampling centers.
//
// A component plane is described by its pixel dimensions, row stride and,
// independently for each axis, a rational sampling center. The sampling
// center is the offset, in reference-grid pixel units, of sample index 0
// relative to the left/top edge of the reference cell 0. For example an
// MPEG-2 horizontally co-sited 4:2:2 chroma plane uses center 0 on X while
// an MPEG-2 4:2:0 chroma plane uses center 1/2 on both axes. There is no
// implicit assumption that chroma and luma share a center: callers must
// provide every center explicitly.
//
// Resampling is a separable area-weighted reconstruction. On each axis the
// continuous kernel, expressed in input-pixel units, is integrated over the
// input cells covered by the output cell; the resulting tap weights are
// normalized before application. Coordinates outside the source extent are
// handled with pixel-mirror ("reflect") boundaries. All arithmetic is kept
// in high precision float64 until the final write, where each value is
// rounded to nearest with ties to even and saturated to the sample range.
package chromaresample
