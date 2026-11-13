// Package netsec implements a fail-closed, pre-copy memory migration
// state machine for a small virtual machine (at most 64 pages).
//
// The package models three important security boundaries:
//
//   - A source page has a monotonically increasing write generation and a
//     content digest.  A dirty bit may only be retired when the source still
//     has exactly the generation that was copied and verified.
//   - Copied pages are placed in a target quarantine.  They are not visible
//     to the target VM until a host-signed receipt binds the VM, both
//     instance identities, the final epoch, every page generation, the
//     device-state digest and the manifest root.
//   - The source remains frozen, and its memory cannot be released, until the
//     target returns a startup confirmation.  A failed hand-off may thaw the
//     source, but the un-started target quarantine is discarded.
package netsec
