// Package readproof implements authenticated, tenant-scoped read planning and
// acceptance for content-addressed concatenation trees.
//
// A manifest node is either a leaf, which names a physical block and a byte
// length, or an internal node, which is an ordered list of subintervals of
// child nodes.  Nodes are identified by SHA-256 over their canonical encoding.
// Repeated child references are normal and are never expanded by this package.
//
// A root is trusted only after the injected signature verifier accepts it.
// Read credentials authorize half-open intervals in root coordinates for one
// tenant, root, and generation.  Physical block reuse never transfers an
// authorization between positions or tenants.
//
// Certificate planning minimizes the number of leaf segments.  At each step it
// chooses the farthest reachable endpoint and, on a tie, the smallest
// credential ID.  Acceptance independently replays every path, checks the
// current credential/revocation view, checks physical placement, and verifies
// exact contiguous coverage.
package readproof
