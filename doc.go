// Package netsec verifies Git signed-push certificates for a Go Git server's
// receive hooks.
//
// ParseCertificate reads a version 0.1 push certificate's pusher, pushee,
// nonce, and ref updates, and separates the exact signed payload from the
// detached signature. Verifier checks that the certificate updates equal the
// pre-receive hook's received updates item by item, authorizes protected refs
// by pusher identity, and atomically consumes each nonce once. Signature
// verification is supplied by the caller through Config.VerifySignature.
//
// A Verifier must be reused for every push (and its NonceStore must be shared
// or durable) so that a replayed certificate cannot reuse a nonce.
package netsec
