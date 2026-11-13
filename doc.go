// Package netsec provides an importable P-256 Elliptic Curve Diffie-Hellman
// library for TLS stacks that enforces strict public-point validation.
//
// Every received peer point is required to satisfy the P-256 curve equation
// y^2 == x^3 - 3x + b (mod p). A point that merely has the expected byte
// length but lies off the curve (for example one with a tampered y
// coordinate) is rejected with ErrInvalidPoint before any scalar
// multiplication runs, preventing invalid-curve attacks. Genuine on-curve
// points complete the key exchange normally.
package netsec
