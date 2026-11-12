// Package netsec implements the bootstrap credential state machine for a Go
// launch host.
//
// When a virtual machine is reclaimed, its network address may be reassigned
// to a fresh instance. Identity files routed by address alone can therefore
// arrive late and must never activate the new instance. A Supervisor binds
// every accepted credential to three trusted values of the currently open
// bootstrap window:
//
//   - the instance identity reported by the instance monitor,
//   - a per-boot random nonce generated when the instance starts, and
//   - a strictly increasing, one-time deployment generation.
//
// BootInstance opens a window for one boot and immediately revokes the
// previous window for the same address after purging the running
// environment. HandleCredential installs a credential only while every
// binding field matches the open window; stale, replayed and windowless
// files are rejected before they reach the runtime sink. Installation uses
// a stage/commit/abort contract so a failed install leaves no credential in
// the running environment.
package netsec
