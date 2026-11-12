// Package taskpkg integrates emission-absorption rays through medical
// volume data.
//
// Each voxel sample provides an absorption coefficient sigma and a linear,
// unclamped RGB emission rate. Samples live at the nodes of a structured
// grid whose node coordinates may be non-uniform on every axis; coefficients
// inside a cell are produced by trilinear interpolation of the eight
// surrounding nodes.
//
// Integration never uses a fixed sampling step. Within each grid cell the
// ray is integrated with analytic constant-coefficient segments
//
//	T = exp(-sigma*h),  C = em * (1-T)/sigma,
//
// composed front-to-back. An adaptive rule compares the whole-segment
// estimate against the two-half-segment estimate and subdivides until their
// discrepancy falls below the requested tolerance, so changing the effective
// step changes only the work, not the converged brightness (the source of
// step-dependent flicker). The zero-absorption case is evaluated through its
// exact limit T=1, C=em*h, using Expm1 for short optical depths.
//
// Cell boundaries of the non-uniform grid are located exactly via an
// incremental slab/DDA crossing computed from the node coordinates. The
// caller supplies a step budget; when it is exhausted, Integrate returns
// immediately with Exhausted=true and conservative remaining-error estimates
// for both radiance and transmittance.
package taskpkg
