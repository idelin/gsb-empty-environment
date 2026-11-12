// Package netsec implements the durable lifecycle of persistent-memory
// regions for a storage control plane.
//
// # Invariants
//
// A region is in exactly one of three states: in-use, erasing, or clean.
// Only clean regions can be allocated.  An authorized erase transitions an
// in-use region to erasing, overwrites every 1 MiB stripe with a fixed pattern,
// reads each stripe back, and only then durably records the clean state.
//
// The erase journal is append-only and fsynced before in-memory state advances.
// A power cut leaves any incomplete erase durably marked erasing; opening the
// manager over the same journal reconstructs per-stripe checkpoints and
// Recover continues from the first stripe that is not confirmed.  A torn final
// journal record is discarded safely, while interior corruption is rejected.
//
// Erase credentials are stored as region-bound hashes (optionally HMACs) and
// compared in constant time.  Consecutive failures are counted per region;
// reaching the configured limit freezes authorization for that region until a
// process restart.  The lockout is intentionally volatile and is not a fourth
// region state.
package netsec
