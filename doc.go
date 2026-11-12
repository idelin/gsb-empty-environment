// Package taskpkg implements rigid dual-quaternion skinning for Go animation
// render hosts.
//
// A bone is represented by a DualQuat. Use FromMatrix4 to validate a matrix as
// a proper rigid transform, or FromRotationTranslation to build one directly.
// BlendDualQuaternions and the SkinPoint/SkinNormal helpers perform weighted
// per-vertex skinning. Weights must be finite, non-negative, and sum to one.
//
// Before blending, quaternion signs are aligned to the maximum-weight bone.
// The weighted real and dual parts are then normalized so the real part has
// unit length and is orthogonal to the dual part. Positions receive the
// blended rotation and translation; normals receive only the blended rotation
// and are renormalized.
package taskpkg
