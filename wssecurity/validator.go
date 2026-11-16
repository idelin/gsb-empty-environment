// Package wssecurity validates WS-Security 1.1 headers of inbound SOAP
// messages at a gateway: timestamp freshness, signature coverage of the
// wsu:Timestamp and SOAP Body, and one-time use of UsernameToken
// Nonce/Created pairs.
//
// XML digital signature verification itself is gateway specific, so it is
// injected through Config.VerifySignature. The package only checks structural
// constraints, time windows and replay state, and confirms that the injected
// verifier lists both the Timestamp and the Body among its signed references.
package wssecurity

import (
	"bytes"
	"context"
	"io"
	"strings"
	"time"
)

// Default limits required by the specification.
const (
	DefaultMaxFutureSkew   = 60 * time.Second
	DefaultMaxTimestampTTL = 300 * time.Second
	// DefaultNonceRetention keeps Nonce/Created pairs long enough to cover the
	// largest legal timestamp window plus the accepted clock skew.
	DefaultNonceRetention = DefaultMaxTimestampTTL + DefaultMaxFutureSkew
)

// VerifySignature is supplied by the caller. It must cryptographically verify
// the XML-DSig/XML-Signature carried in the message and return the set of
// signed references. References may be bare ids ("ts-1") or fragment URIs
// ("#ts-1"); both forms are accepted.
//
// A false return, or an error, rejects the message.
type VerifySignature func(ctx context.Context, r io.Reader) (signedRefs []string, ok bool, err error)

// Config configures a Validator. Zero durations fall back to the package
// defaults, which match the required 60s skew and 300s lifetime.
type Config struct {
	VerifySignature VerifySignature
	NonceCache      NonceCache

	// MaxFutureSkew is how far in the future wsu:Created may be.
	MaxFutureSkew time.Duration
	// MaxTimestampTTL is the maximum allowed Expires-Created interval.
	MaxTimestampTTL time.Duration
	// Now is the clock source. Defaults to time.Now.
	Now func() time.Time
}

// Validator is safe for concurrent use. Create one with New and reuse it.
type Validator struct {
	verify  VerifySignature
	nonces  NonceCache
	maxSkew time.Duration
	maxTTL  time.Duration
	now     func() time.Time
}

// New builds a Validator. VerifySignature is mandatory; a nil NonceCache is
// replaced with an in-memory MemoryNonceCache.
func New(cfg Config) (*Validator, error) {
	if cfg.VerifySignature == nil {
		return nil, validationError(ErrSignatureVerify, "Config.VerifySignature is required")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	maxSkew := cfg.MaxFutureSkew
	if maxSkew <= 0 {
		maxSkew = DefaultMaxFutureSkew
	}

	maxTTL := cfg.MaxTimestampTTL
	if maxTTL <= 0 {
		maxTTL = DefaultMaxTimestampTTL
	}

	cache := cfg.NonceCache
	if cache == nil {
		cache = NewMemoryNonceCache(DefaultNonceRetention)
	}

	return &Validator{
		verify:  cfg.VerifySignature,
		nonces:  cache,
		maxSkew: maxSkew,
		maxTTL:  maxTTL,
		now:     now,
	}, nil
}

// Validate enforces every WS-Security 1.1 rule on one SOAP message and marks
// any UsernameToken Nonce/Created pair as consumed. All failures produce a
// *ValidationError whose Code is one of the package's sentinel errors.
func (v *Validator) Validate(ctx context.Context, r io.Reader) error {
	// Buffer once: the XML parser and the injected signature verifier both
	// need the raw bytes, and the verifier may replay readers internally.
	raw, err := io.ReadAll(r)
	if err != nil {
		return validationError(ErrMalformed, "%s", err.Error())
	}

	msg, err := parseMessage(bytes.NewReader(raw))
	if err != nil {
		return err
	}

	// 1. Structural constraints: exactly one Security/Timestamp/Body.
	if msg.securityHeaders == 0 {
		return validationError(ErrNoSecurityHeader, "message has no wsse:Security header")
	}
	switch len(msg.timestamps) {
	case 0:
		return validationError(ErrNoTimestamp, "wsse:Security contains no wsu:Timestamp")
	case 1:
		// ok
	default:
		return validationError(ErrMultipleTimestamps, "found %d wsu:Timestamp elements", len(msg.timestamps))
	}
	switch msg.bodies {
	case 0:
		return validationError(ErrNoBody, "message has no SOAP Body")
	case 1:
		// ok
	default:
		return validationError(ErrMultipleBodies, "found %d SOAP Body elements", msg.bodies)
	}

	ts := msg.timestamps[0]
	if ts.ID == "" {
		return validationError(ErrMissingID, "wsu:Timestamp has no wsu:Id")
	}
	if msg.bodyID == "" {
		return validationError(ErrMissingID, "SOAP Body has no wsu:Id")
	}

	// 2. wsu:Id uniqueness across the whole document.
	if dup, ok := msg.repeatedID(); ok {
		return validationError(ErrDuplicateID, "wsu:Id %q appears more than once", dup)
	}

	// 3. The injected verifier must cover both references with real signatures.
	refs, ok, err := v.verify(ctx, bytes.NewReader(raw))
	if err != nil {
		return validationError(ErrSignatureVerify, "%s", err.Error())
	}
	if !ok {
		return validationError(ErrSignatureVerify, "signature verification returned false")
	}
	signed := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		signed[strings.TrimPrefix(strings.TrimSpace(ref), "#")] = struct{}{}
	}
	if _, covered := signed[ts.ID]; !covered {
		return validationError(ErrUnsignedTimestamp, "signed references do not include %q", ts.ID)
	}
	if _, covered := signed[msg.bodyID]; !covered {
		return validationError(ErrUnsignedBody, "signed references do not include %q", msg.bodyID)
	}

	// 4. Timestamp freshness and lifetime.
	now := v.now()
	created, err := parseTime(ts.Created)
	if err != nil {
		return validationError(ErrInvalidTime, "wsu:Created: %v", err)
	}
	expires, err := parseTime(ts.Expires)
	if err != nil {
		return validationError(ErrInvalidTime, "wsu:Expires: %v", err)
	}

	if created.Sub(now) > v.maxSkew {
		return validationError(ErrCreatedInFuture, "wsu:Created %s is more than %s ahead of now %s",
			created.Format(time.RFC3339Nano), v.maxSkew, now.Format(time.RFC3339Nano))
	}
	if !expires.After(created) {
		return validationError(ErrExpiresOrder, "wsu:Expires %s is not later than wsu:Created %s",
			expires.Format(time.RFC3339Nano), created.Format(time.RFC3339Nano))
	}
	if expires.Sub(created) > v.maxTTL {
		return validationError(ErrTimestampLifetime, "lifetime %s exceeds maximum %s",
			expires.Sub(created), v.maxTTL)
	}
	if !now.Before(expires) {
		return validationError(ErrTimestampExpired, "wsu:Expires %s is before now %s",
			expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	}

	// 5. UsernameToken Nonce/Created replay protection.
	for _, token := range msg.tokens {
		if token.Nonce == "" {
			return validationError(ErrMissingNonce, "UsernameToken %q has no wsse:Nonce", token.ID)
		}
		if token.Created == "" {
			return validationError(ErrMissingUTCreated, "UsernameToken %q has no wsu:Created", token.ID)
		}

		ttl := DefaultNonceRetention
		if utCreated, perr := parseTime(token.Created); perr == nil {
			// Keep the pair until its own validity window can no longer overlap
			// with any accepted timestamp.
			remaining := expires.Sub(utCreated) + v.maxSkew
			if remaining > ttl {
				ttl = remaining
			}
		}

		firstUse, err := v.nonces.CheckAndStore(ctx, nonceKey(token.Nonce, token.Created), now, ttl)
		if err != nil {
			return validationError(ErrReplayedNonce, "nonce cache error: %v", err)
		}
		if !firstUse {
			return validationError(ErrReplayedNonce, "Nonce/Created pair already used in this window")
		}
	}

	return nil
}

// parseTime accepts the xsd:dateTime lexical form produced by WS-Security,
// i.e. RFC 3339 with optional fractional seconds and a mandatory timezone.
// Values without a timezone are rejected rather than guessed.
func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, value)
}

func nonceKey(nonce, created string) string {
	return nonce + "|" + created
}
