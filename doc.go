// Package netsec provides admission control for SUCI identities received by a
// 5G home network component.
//
// The package rejects the null protection scheme unless the caller marks the
// registration as an emergency, accepts only protection schemes 0, 1 (Profile
// A), and 2 (Profile B), requires the home-network public key identifier to
// exist in the current or previous generation key table, verifies the MAC
// before invoking the injected decryptor, and rejects repeated ephemeral
// public keys inside a configurable replay window.
package netsec
