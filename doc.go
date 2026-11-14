// Package netsec implements signed, short-lived, one-time single-logout
// tokens plus the back-channel and front-channel handlers that consume them.
//
// Logout tokens are compact JWS values carrying iss, aud, sid, sub, iat, exp
// and jti. Only allow-listed issuers and their registered key IDs are
// accepted. Every token is consumed exactly once by a concurrency-safe replay
// cache, and a logout terminates only the session named by sid.
package netsec
