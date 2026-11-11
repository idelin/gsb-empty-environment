// Package vlan implements the bounded 802.1Q label-stack rewrite used on the
// virtual switch receive path.
//
// # Stack bound
//
// The datapath only handles zero, one (priority or VLAN) or two
// (service + customer) tags. ParseFrame rejects anything deeper with
// ErrTagStackTooDeep rather than guessing which tag owns the frame, and the
// tunnel port refuses ingress that would push the stack past two tags.
//
// # Port modes
//
// Three port modes decide which wire shapes are admitted and how canonical
// frames are mapped back to the wire:
//
//   - ModeAccess accepts untagged/priority frames and binds them to one
//     AccessVID; egress strips the VID (PCP is emitted as a priority tag).
//   - ModeTrunk accepts allowed single/double tags and optional native-VLAN
//     untagged frames; egress emits the same stack, native VLAN untagged.
//   - ModeTunnel wraps customer frames in an S-TAG carrying AccessVID on
//     ingress and pops it on egress.
//
// # Consistency under policy updates
//
// A Policy is an immutable, validated snapshot. Switch.Swap installs it with
// an atomic pointer store, so every frame is processed under exactly one
// snapshot. All rewrites allocate fresh byte slices (Frame.Marshal,
// insertTag); input frames are never mutated and concurrent swaps cannot
// expose half-rewritten bytes. Egress additionally verifies the frame's tag
// stack matches the tenant it was classified under, preventing a frame from
// being delivered onto another tenant's port.
package vlan
