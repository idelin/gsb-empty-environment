// Package netsec provides request authorization for an SFTP server,
// including the SSH_FXP_EXTENDED requests that are frequently used to bypass
// authorization written only for the basic request set.
//
// Every request is reduced to the same two primitives — PermRead and
// PermWrite on a path — and then passes through one shared pipeline:
//
//   - virtual SFTP path normalization against the configured root,
//   - lexical containment inside the root,
//   - symlink resolution through the caller-injected FS and a second
//     containment check on the resolved real path,
//   - the read-only switch, which rejects every write-class check,
//   - the FS.Access decision of the actual host file system.
//
// The extended requests are mapped as follows:
//
//   - posix-rename@openssh.com: write on the source directory and write on
//     the target directory;
//   - hardlink@openssh.com: read on the source file and write on the
//     link's parent directory;
//   - copy-data: read on the read handle's file and write on the write
//     handle's file;
//   - fsetstat@openssh.com: write on the handle's file.
//
// Any other extended name, including forged names carrying an "@openssh.com"
// or other vendor suffix, fails with SSH_FX_OP_UNSUPPORTED. The file system
// is intentionally accessed only through the FS interface so that callers
// inject their own root handling, identity checks and handle table.
package netsec
