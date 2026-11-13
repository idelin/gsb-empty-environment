// Package netsec lets a TLS stack tolerate compatibility ChangeCipherSpec
// records without letting them into the handshake transcript: a CCS never
// feeds the transcript hash, never rotates keys, is rejected at illegal
// positions, leaves Finished verify_data untouched at legal positions, and
// is tolerated exactly once more after Finished.
package netsec
