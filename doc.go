// Package netsec implements fail-closed admission checks for OPC UA
// ActivateSession user identity tokens.
//
// It verifies that a requested PolicyId is declared by the endpoint, that a
// UserName token is encrypted with the policy's effective security policy, and
// that a Certificate token signs the server certificate followed by a
// single-use server nonce.
package netsec
