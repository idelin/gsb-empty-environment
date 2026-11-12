package netsec

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// SignatureVerifier verifies signature over payload. It must return nil only
// when the signature is valid for the given signed payload. The package does
// not choose or implement a signature algorithm; callers can invoke gpg,
// ssh-keygen, or a library-backed verifier.
type SignatureVerifier func(payload, signature []byte) error

// Config configures a Verifier.
type Config struct {
	// VerifySignature is required and verifies the detached signature over
	// Certificate.Payload.
	VerifySignature SignatureVerifier

	// Nonces records push nonces. If nil, NewVerifier creates an in-memory
	// store. Production services should provide a durable store and share one
	// Verifier (or one store) across pushes.
	Nonces NonceStore

	// Pushee, when non-empty, must exactly match Certificate.Pushee. This
	// prevents a certificate issued for one repository from being replayed
	// against another.
	Pushee string

	// ProtectedRefs maps ref patterns to the pusher identities that may update
	// them. A pattern may be an exact ref, a path.Match pattern such as
	// "refs/heads/*", or a prefix ending in "*" such as "refs/heads/release/*".
	// If several patterns match a ref, any pusher listed for a matching pattern
	// is authorized.
	ProtectedRefs map[string][]string
}

// Verifier checks signed push certificates against the updates received by a
// pre-receive hook. Create one Verifier per repository (or share the NonceStore
// across repositories) and reuse it for every push.
type Verifier struct {
	verifySignature SignatureVerifier
	nonces          NonceStore
	pushee          string
	protectedRefs   []protectedRefRule
}

type protectedRefRule struct {
	pattern string
	pushers []string
}

// NewVerifier builds a Verifier. A signature callback is mandatory.
func NewVerifier(config Config) (*Verifier, error) {
	if config.VerifySignature == nil {
		return nil, fmt.Errorf("%w: VerifySignature callback is required", ErrInvalidConfig)
	}

	nonces := config.Nonces
	if nonces == nil {
		nonces = NewMemoryNonceStore()
	}

	verifier := &Verifier{
		verifySignature: config.VerifySignature,
		nonces:          nonces,
		pushee:          config.Pushee,
	}
	for pattern, pushers := range config.ProtectedRefs {
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("%w: empty protected ref pattern", ErrInvalidConfig)
		}
		verifier.protectedRefs = append(verifier.protectedRefs, protectedRefRule{
			pattern: pattern,
			pushers: append([]string(nil), pushers...),
		})
	}
	return verifier, nil
}

// Verify validates certificate against the exact updates the hook received and
// returns the parsed certificate on success.
//
// Verification checks, in order:
//  1. the certificate structure and required metadata;
//  2. an optional configured pushee;
//  3. item-by-item equality between certificate updates and received updates;
//  4. the detached signature through the callback;
//  5. protected-ref authorization by pusher identity;
//  6. atomic single use of the nonce.
//
// The nonce is consumed only after all other checks pass, so malformed,
// unsigned, mismatched, or unauthorized pushes do not burn a valid nonce.
func (v *Verifier) Verify(certificate []byte, updates []Update) (*Certificate, error) {
	cert, err := ParseCertificate(certificate)
	if err != nil {
		return nil, err
	}

	if v.pushee != "" && cert.Pushee != v.pushee {
		return nil, fmt.Errorf(
			"%w: certificate is for %q, repository is %q",
			ErrPusheeMismatch, cert.Pushee, v.pushee,
		)
	}
	if err := CompareUpdates(cert.Updates, updates); err != nil {
		return nil, err
	}
	if err := v.verifySignature(cert.Payload, cert.Signature); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignatureVerification, err)
	}
	if err := v.authorizeUpdates(cert.Pusher, updates); err != nil {
		return nil, err
	}
	if err := consumeNonce(v.nonces, cert.Nonce); err != nil {
		return nil, err
	}

	return cert, nil
}

// VerifyReader parses the pre-receive hook's standard input, then calls
// Verify.
func (v *Verifier) VerifyReader(certificate []byte, updates io.Reader) (*Certificate, error) {
	parsedUpdates, err := ParseUpdates(updates)
	if err != nil {
		return nil, err
	}
	return v.Verify(certificate, parsedUpdates)
}

func (v *Verifier) authorizeUpdates(pusher string, updates []Update) error {
	for _, update := range updates {
		matches, authorized := false, false
		for _, rule := range v.protectedRefs {
			if !matchRef(rule.pattern, update.Ref) {
				continue
			}
			matches = true
			for _, allowed := range rule.pushers {
				if allowed == pusher {
					authorized = true
					break
				}
			}
		}
		if matches && !authorized {
			return &UnauthorizedPusherError{Ref: update.Ref, Pusher: pusher}
		}
	}
	return nil
}

func matchRef(pattern, ref string) bool {
	if pattern == ref {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(ref, strings.TrimSuffix(pattern, "*"))
	}
	matched, err := path.Match(pattern, ref)
	return err == nil && matched
}

// UnauthorizedPusherError identifies a protected ref update by a pusher who is
// not authorized for that ref.
type UnauthorizedPusherError struct {
	Ref    string
	Pusher string
}

func (e *UnauthorizedPusherError) Error() string {
	return fmt.Sprintf(
		"netsec: pusher %q is not authorized to update protected ref %q",
		e.Pusher, e.Ref,
	)
}

func (e *UnauthorizedPusherError) Unwrap() error {
	return ErrUnauthorizedPusher
}

var (
	// ErrInvalidConfig indicates an unusable Verifier configuration.
	ErrInvalidConfig = errors.New("netsec: invalid verifier configuration")

	// ErrPusheeMismatch indicates that the certificate was issued for a
	// different repository.
	ErrPusheeMismatch = errors.New("netsec: push certificate pushee mismatch")

	// ErrSignatureVerification indicates that the signature callback rejected
	// the certificate.
	ErrSignatureVerification = errors.New("netsec: push certificate signature verification failed")

	// ErrUnauthorizedPusher indicates an update to a protected ref by a pusher
	// that is not on the ref's allowlist.
	ErrUnauthorizedPusher = errors.New("netsec: pusher is not authorized for protected ref")
)
