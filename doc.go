// Package netsec verifies Linux EVM security metadata before a file is
// accessed.
//
// An EVM security.evm extended attribute binds the file's security.ima and
// security.selinux values together with the inode number, i_generation, uid,
// gid and mode. Two formats are supported:
//
//   - EVMTypeHMAC (type byte 0x02) is verified by recomputing HMAC-SHA256 with
//     an HMAC key loaded into this process. The MAC is bound to the inode and
//     i_generation as well as the attribute values.
//   - EVMTypePortableSignature (type byte 0x05) is verified with a trusted
//     Ed25519 public key. Its payload intentionally omits the inode and
//     i_generation so that the signature remains portable between filesystems
//     and machines.
//
// Callers read the current inode metadata and the security.evm value
// immediately before granting access, then call Evaluator.Evaluate (or
// Evaluator.Verify). The package is fail-closed: a missing HMAC key, a missing
// trust anchor, an unknown type byte, a malformed value or any authentication
// failure denies access. It never treats security.ima as an alternative EVM
// check.
package netsec
