// Package taskpkg stitches adaptively tessellated quadrilateral patches
// into a single crack-free triangle mesh for tessellation rendering hosts.
//
// The input is a set of quad patches sharing edges, each with a
// power-of-two tessellation level. Levels are first propagated so that
// neighbours differ by at most one level, then every patch is triangulated
// with deterministic transition triangles along finer neighbouring edges.
// Points on shared edges are identified solely by the shared edge ID and a
// rational parameter along it, so coincident positions computed by either
// side are bitwise identical. Only orientable manifold meshes are
// accepted; non-manifold input is rejected.
package taskpkg
