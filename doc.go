// Package netsec implements importable multi-device two-party safety numbers.
//
// A device-list snapshot binds every device ID, identity public key, and added
// version for an account. Account identity digests therefore change when a
// server inserts, removes, hides, or rekeys a device, and the two-party safety
// number changes with them. QR payloads bind both accounts' list versions and
// digests plus the raw 32-byte checksum; verification checks the complete
// account set and recomputes the checksum from current local snapshots.
//
// Verification and confirmation state is never silently reused across list
// changes. A newer or different complete device set makes the old state Stale.
package netsec
