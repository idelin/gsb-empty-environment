// Package passkey implements a standard-library-only WebAuthn (passkey) login
// completion endpoint.
//
// It verifies ES256 (ECDSA over P-256/SHA-256) assertion signatures, binds
// every assertion to the relying party ID, expected origin and single-use
// challenge, distinguishes backup-credential zero counters from single-device
// counter fallback, and issues at most one session per challenge even when two
// synchronized devices answer concurrently.
//
// All cryptographic and binding checks are performed before the challenge is
// deleted, so an invalid signature or counter replay never burns the
// challenge. The final challenge consumption is an atomic operation guarded by
// ChallengeStore's mutex, which is what guarantees single-session semantics
// under concurrent duplicate assertions.
//
// Minimal wiring:
//
//	svc := &passkey.Service{
//	    RP:          passkey.RelyingParty{ID: "example.com", Origin: "https://example.com"},
//	    Challenges:  passkey.NewChallengeStore(),
//	    Credentials: passkey.NewCredentialStore(),
//	    Sessions:    passkey.NewSessionStore(),
//	}
//	http.Handle("POST /login/complete", passkey.NewLoginCompleteHandler(svc))
package passkey
