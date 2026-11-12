// Package taskpkg implements world-space ray picking for scenes made of
// shared, instanced triangle meshes with invertible affine transforms.
//
// Meshes own an immutable local-space BVH (BLAS). A Scene owns a second,
// world-space BVH (TLAS) over instances. Transform updates refit the TLAS
// without duplicating or rebuilding shared mesh data.
//
// Queries return the world ray parameter T, so hits from anisotropically or
// non-uniformly scaled instances are comparable directly. Normals are
// transformed by the inverse transpose. Equal-distance hits are ordered by
// (InstanceID, TriangleID).
package taskpkg
