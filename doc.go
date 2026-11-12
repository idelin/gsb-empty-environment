// Package taskpkg builds closed point-light shadow volumes for an offscreen
// rendering host.
//
// A shadow volume is the closed triangular region swept by an occluder as
// seen from a point light. This package takes a closed, consistently oriented
// triangle mesh and a point light guaranteed not to lie on the mesh surface,
// classifies each face as lit or back-facing from its exact orientation
// relative to the light, extracts the silhouette edges shared by a lit and a
// non-lit face, extrudes those edges along the light rays until they meet an
// explicit far plane, and caps the result near (the lit faces) and far (their
// reversed extruded copies). Every shared extrusion endpoint is allocated
// once and reused by vertex ID, so the output is a single watertight,
// outward-oriented closed mesh.
//
// All orientation, coplanarity, intersection and containment decisions use
// exact math/big.Rat arithmetic; no floating-point predicate is used for
// topology. The only convention needed for degenerate inputs is a fixed sign
// perturbation: when the light lies exactly in a triangle plane the
// determinant is zero and FaceSide deterministically treats that face as
// back-facing. Applying the same rule to every face keeps the silhouette a
// valid separator and the resulting volume closed.
//
// The companion reference functions ClassifyPoint and OccludedByRay provide
// independent exact tests used to verify, for every non-boundary camera point
// inside and outside the volume, that volumetric containment and per-ray
// occlusion agree.
package taskpkg
