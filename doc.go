// Package netsec provides authenticated encryption whose key agreement
// context binds the sender (partyU) and the recipient (partyV) in that exact
// order.
//
// The same ECDH shared secret or pre-shared secret is deliberately not enough
// to open a message: the pair (partyU, partyV) is length-prefixed and fed to
// HKDF as "U" followed by "V". Reversing the pair therefore derives a
// different content-encryption key and AES-GCM authentication fails.
package netsec
