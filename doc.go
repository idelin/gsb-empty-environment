// Package netsec provides a pre-instantiation type-exposure check for Go
// hosts embedding an untrusted Wasm GC module.
//
// Threat model: the host hands the module a reference to a struct with
// private fields. Wasm GC canonicalizes recursive groups structurally, so a
// module that declares a group equivalent to the host's private type can
// ref.cast the received reference to its own type and read the private
// fields. CheckModule rejects such modules before instantiation.
//
// Equivalence is decided by structural normalization of the whole recursive
// group, never by type or member names: two groups are equivalent iff their
// members can be put into a one-to-one correspondence such that
// corresponding structs agree on the final flag, the supertype, and the
// exact field sequence (kind, order and mutability), with intra-group
// references mapped through the correspondence. References to earlier types
// outside the group (including supertypes) must point at the same absolute
// type index; callers should therefore describe the module's type space on
// the same index basis as the host's (a module reconstructing the host's
// earlier types at the same indices is exactly the attack being caught).
// Any difference in field order, mutability, final flag, supertype or
// reference target makes the groups non-equivalent.
package netsec
