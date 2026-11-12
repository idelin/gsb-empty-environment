// Package netsec verifies key-transparency prefix-tree proofs on the
// client side. User labels are hidden behind a VRF whose proof is
// verified by a caller-injected VRFVerifier; the VRF output is walked
// bit by bit through a Merkle prefix tree. Internal nodes, leaves and
// empty subtrees are hashed under distinct domain tags that include the
// node depth, and epoch roots are only accepted in strictly increasing
// epoch order.
package netsec
