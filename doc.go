// Package netsec validates Android key attestation material for device
// registration.
//
// Certificate-chain verification is deliberately left to the caller. This
// package parses the leaf certificate's KeyDescription extension, verifies
// that authorization claims are protected by teeEnforced, and checks the
// registration challenge and verified boot state.
package netsec
