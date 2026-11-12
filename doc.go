// Package taskpkg implements geometrically validated temporal reprojection
// for a Go rendering pipeline.
//
// Reproject resolves previous-pixel history through previous and current
// camera matrices, per-object affine motion, depth, object generation and
// world-space normals. Cross-object and rear-layer history is rejected before
// it can be blended into a front-layer pixel.
package taskpkg
