// Package netsec implements a two-server XOR PIR protocol for 32-byte
// records with Ed25519-signed server responses and per-row HMAC integrity.
//
// The privacy guarantee is the standard two-server, non-colluding model: one
// server alone sees a uniformly distributed query vector independent of the
// requested index. The package does not claim security against two servers
// that exchange their query vectors.
package netsec
