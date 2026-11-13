// Package netsec implements a COSE_Encrypt-inspired encryption layer that
// binds every recipient to its key-agreement context. Decrypted plaintext
// is only returned to the caller when the recipient context matches the
// caller's session context; otherwise it is withheld.
package netsec
