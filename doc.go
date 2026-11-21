// Package admission verifies and certifies virtual-to-physical block mappings.
//
// A Graph is an immutable description of one or more virtual volume roots,
// the subvolume mappings reachable from those roots, and the physical ranges
// owned by the tenant. Compile checks reachability, cycles, offset overflow,
// permissions, and physical ownership. A compiled Snapshot can produce a
// Proof made of virtual arithmetic classes and their physical image classes.
//
// The package never expands a mapping sector by sector. All interval and
// arithmetic-class operations are performed on their endpoints and strides.
package admission
