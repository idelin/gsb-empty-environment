package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

// SignatureResult is the outcome of the caller's XML-DSIG verification.
type SignatureResult struct {
	// SignedReferences holds the Id references that the signature actually
	// covers. Both "#id" and bare "id" forms are accepted.
	SignedReferences []string
}

// SignatureVerifier is injected by the gateway. It performs the cryptographic
// XML signature verification (canonicalization, digest and signature value
// checks) and reports which element Ids were signed. The validator only
// accepts messages whose Timestamp and Body Ids are in that set.
type SignatureVerifier func(ctx context.Context, message []byte) (*SignatureResult, error)

// Validator enforces the WS-Security 1.1 header policy. A Validator is safe
// for concurrent use once constructed, except when an injected clock or
// verifier is itself unsafe.
type Validator struct {
	verify SignatureVerifier
	nonces NonceStore
	now    func() time.Time

	// MaxCreatedSkew bounds how far in the future wsu:Created may be.
	MaxCreatedSkew time.Duration
	// MaxTimestampTTL bounds the Created-to-Expires interval.
	MaxTimestampTTL time.Duration
}

// New builds a Validator. verify is required. nonces is required whenever
// inbound messages may carry a UsernameToken; pass nil only for deployments
// that never accept one, in which case a UsernameToken is rejected.
func New(verify SignatureVerifier, nonces NonceStore) *Validator {
	v := &Validator{
		verify:          verify,
		nonces:          nonces,
		MaxCreatedSkew:  60 * time.Second,
		MaxTimestampTTL: 300 * time.Second,
	}
	v.now = time.Now
	return v
}

// withDefaults normalizes zero-valued configuration so callers may construct a
// Validator with a zero value plus exported fields.
func (v *Validator) withDefaults() {
	if v.MaxCreatedSkew <= 0 {
		v.MaxCreatedSkew = 60 * time.Second
	}
	if v.MaxTimestampTTL <= 0 {
		v.MaxTimestampTTL = 300 * time.Second
	}
	if v.now == nil {
		v.now = time.Now
	}
}

// SetClock overrides the time source. It exists for deterministic tests and
// for gateways that keep a managed clock; production callers normally leave
// the default wall clock in place.
func (v *Validator) SetClock(now func() time.Time) {
	if now != nil {
		v.now = now
	}
}

// Validate runs the full policy against one SOAP message and returns nil on
// acceptance. Every rejection is a *ValidationError wrapping a stable
// sentinel, so callers can map failures to SOAP faults with errors.Is.
func (v *Validator) Validate(ctx context.Context, r io.Reader) error {
	v.withDefaults()

	raw, err := io.ReadAll(r)
	if err != nil {
		return fail(ErrInvalidMessage, "reading message: %v", err)
	}

	msg, err := parseMessage(strings.NewReader(string(raw)))
	if err != nil {
		return err
	}

	if v.verify == nil {
		return fail(ErrConfiguration, "no SignatureVerifier configured")
	}
	result, err := v.verify(ctx, raw)
	if err != nil {
		return fail(ErrSignatureVerify, "verifier rejected message: %v", err)
	}
	if result == nil {
		return fail(ErrSignatureVerify, "verifier returned no signature result")
	}
	signed := make(map[string]struct{}, len(result.SignedReferences))
	for _, ref := range result.SignedReferences {
		signed[normalizeRef(ref)] = struct{}{}
	}

	if err := v.validateTimestamp(msg, signed); err != nil {
		return err
	}
	if err := v.validateBody(msg, signed); err != nil {
		return err
	}
	return v.validateUsernameTokens(msg)
}

func (v *Validator) validateTimestamp(msg *parsedMessage, signed map[string]struct{}) error {
	switch len(msg.timestamps) {
	case 0:
		return fail(ErrMissingTimestamp, "Security header contains no Timestamp")
	case 1:
	default:
		return fail(ErrDuplicateTimestamp, "found %d Timestamp elements", len(msg.timestamps))
	}

	ts := msg.timestamps[0]
	if !ts.createdOK || !ts.expiresOK {
		return fail(ErrMalformedTimestamp, "Timestamp requires both Created and Expires")
	}
	if ts.id == "" {
		return fail(ErrMissingTimestampID, "Timestamp must carry a wsu:Id")
	}
	if msg.ids[ts.id] > 1 {
		return fail(ErrDuplicateID, "wsu:Id %q appears %d times", ts.id, msg.ids[ts.id])
	}

	now := v.now()
	if skew := ts.created.Sub(now); skew > v.MaxCreatedSkew {
		return fail(ErrCreatedInFuture, "Created is %s ahead of current time, limit %s",
			skew.Round(time.Second), v.MaxCreatedSkew)
	}

	ttl := ts.expires.Sub(ts.created)
	if ttl <= 0 {
		return fail(ErrInvalidTTL, "Expires must be after Created")
	}
	if ttl > v.MaxTimestampTTL {
		return fail(ErrInvalidTTL, "timestamp lifetime %s exceeds limit %s",
			ttl.Round(time.Second), v.MaxTimestampTTL)
	}

	if !now.Before(ts.expires) {
		return fail(ErrExpired, "Timestamp expired at %s", ts.expires.UTC().Format(time.RFC3339))
	}

	if _, ok := signed[ts.id]; !ok {
		return fail(ErrUnsignedTimestamp, "Timestamp Id %q is not a signed reference", ts.id)
	}
	return nil
}

func (v *Validator) validateBody(msg *parsedMessage, signed map[string]struct{}) error {
	if !msg.bodyPresent {
		return fail(ErrInvalidMessage, "message has no SOAP Body")
	}
	if msg.bodyID == "" {
		return fail(ErrMissingBodyID, "SOAP Body must carry a wsu:Id")
	}
	if msg.ids[msg.bodyID] > 1 {
		return fail(ErrDuplicateID, "wsu:Id %q appears %d times",
			msg.bodyID, msg.ids[msg.bodyID])
	}
	if _, ok := signed[msg.bodyID]; !ok {
		return fail(ErrUnsignedBody, "Body Id %q is not a signed reference", msg.bodyID)
	}
	return nil
}

func (v *Validator) validateUsernameTokens(msg *parsedMessage) error {
	for _, token := range msg.tokens {
		if !token.nonceOK || !token.createdOK || token.nonce == "" {
			return fail(ErrMalformedUsernameToken, "UsernameToken requires Nonce and Created")
		}

		now := v.now()
		remaining := token.created.Add(v.MaxTimestampTTL).Sub(now)
		if remaining <= 0 {
			return fail(ErrReplay, "UsernameToken window has elapsed")
		}

		if v.nonces == nil {
			return fail(ErrConfiguration, "no NonceStore configured for UsernameToken")
		}

		key := replayKey(token.nonce, token.created)
		if err := v.nonces.CheckAndStore(key, remaining); err != nil {
			return err
		}
	}
	return nil
}

// normalizeRef strips the XML-DSIG reference fragment marker so "#id-1" and
// "id-1" both compare against the document's bare wsu:Id values.
func normalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "#")
	return ref
}

// replayKey derives a fixed-size key from the exact (Nonce, Created) pair. A
// null separator and length-prefixed fields make the encoding unambiguous;
// hashing keeps stored keys constant-sized.
func replayKey(nonce string, created time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d",
		len(nonce), nonce, created.UnixNano())))
	return hex.EncodeToString(sum[:])
}
