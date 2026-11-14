// Package netsec implements fail-closed admission of MLS Welcome messages.
//
// Welcome admission binds the decrypted group secrets, GroupInfo, ratchet tree,
// transcript, PSKs, KeyPackage and local private key before atomically
// consuming the one-time KeyPackage and advancing durable group state.
// Cryptographic operations are supplied through the Crypto interface.
package netsec
