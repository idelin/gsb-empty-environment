// Package netsec implements sender-key state for end-to-end encrypted group
// chats.
//
// Each sender device owns one symmetric ratchet chain per epoch. GroupState
// enforces the security lifecycle around those chains:
//
//   - Chains are installed only from administrator Ed25519-signed
//     distributions whose epoch is current, whose signed member-set digest
//     matches the current members, and whose sender is a current member.
//   - Chain IDs are unique for the entire lifetime of a group, across every
//     epoch.
//   - Message keys are HMAC-SHA256("msg"||index) derivations and chain keys
//     ratchet via HMAC-SHA256("chain"||index); AES-GCM associated data binds
//     the full header and the epoch's member-set digest.
//   - At most MaxSkippedKeys skipped message keys are cached, each consumed at
//     most once, and authentication failures never advance state.
//   - A member-set change increments the epoch by one, revokes every old
//     chain and permanently rejects all messages from prior epochs, so a
//     removed device cannot keep speaking on its old chain.
//
// All GroupState methods are safe for concurrent use.
package netsec
