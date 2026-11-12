// Package netsec authorizes SFTP operations against a single root directory.
//
// Basic operations and SSH_FXP_EXTENDED requests share one permission model:
// every path is normalized, checked for root confinement including symlink
// targets, rejected in read-only mode when write access is required, and then
// checked by an injected AccessPolicy. Unknown extensions are rejected with
// SSH_FX_OP_UNSUPPORTED rather than being admitted merely because their name
// contains an @domain suffix.
package netsec
