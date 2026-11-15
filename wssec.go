// Package netsec validates WS-Security 1.1 Security headers in SOAP messages.
//
// A Validator checks the Timestamp limits and signed references, verifies the
// SOAP Body and Timestamp wsu:Id values through a caller-supplied XML
// signature verifier, and prevents UsernameToken Nonce/Created replay through
// a NonceReplayStore.
package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	soap11Namespace = "http://schemas.xmlsoap.org/soap/envelope/"
	soap12Namespace = "http://www.w3.org/2003/05/soap-envelope"
	wsseNamespace   = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	wsuNamespace    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"

	// MaxClockSkew is the maximum amount by which a Timestamp Created value
	// may be ahead of the validator's clock.
	MaxClockSkew = 60 * time.Second
	// MaxTimestampLifetime is the maximum allowed Expires-Created interval.
	MaxTimestampLifetime = 300 * time.Second
)

var (
	// ErrNoSignatureVerifier is returned when no XML signature verifier was
	// supplied.
	ErrNoSignatureVerifier = errors.New("ws-security: no signature verifier configured")
	// ErrMalformedXML is returned when the SOAP document cannot be parsed
	// safely as XML.
	ErrMalformedXML = errors.New("ws-security: malformed XML")
	// ErrMalformedSOAP is returned when the expected SOAP 1.1/1.2 envelope
	// structure is absent.
	ErrMalformedSOAP = errors.New("ws-security: malformed SOAP envelope")
	// ErrSecurityHeader is returned when there is not exactly one Security
	// header in the SOAP Header.
	ErrSecurityHeader = errors.New("ws-security: invalid Security header")
	// ErrTimestampCount is returned when there is not exactly one Timestamp.
	ErrTimestampCount = errors.New("ws-security: expected exactly one Timestamp")
	// ErrTimestampFields is returned when Created or Expires is missing,
	// duplicated, or malformed.
	ErrTimestampFields = errors.New("ws-security: invalid Timestamp fields")
	// ErrTimestampCreatedInFuture is returned when Created is more than the
	// allowed clock skew ahead of the validator's clock.
	ErrTimestampCreatedInFuture = errors.New("ws-security: Timestamp Created is in the future")
	// ErrTimestampExpired is returned when the Timestamp has expired.
	ErrTimestampExpired = errors.New("ws-security: Timestamp expired")
	// ErrTimestampLifetime is returned when Expires is not after Created or
	// the Expires-Created interval exceeds the permitted lifetime.
	ErrTimestampLifetime = errors.New("ws-security: invalid Timestamp lifetime")
	// ErrWSUID is returned when a required wsu:Id is missing, duplicated on
	// an element, or malformed.
	ErrWSUID = errors.New("ws-security: invalid wsu:Id")
	// ErrDuplicateID is returned when the same wsu:Id value occurs more than
	// once in the document.
	ErrDuplicateID = errors.New("ws-security: duplicate wsu:Id")
	// ErrTimestampUnsigned is returned when the Timestamp wsu:Id is absent
	// from the verifier's signed-reference set.
	ErrTimestampUnsigned = errors.New("ws-security: Timestamp is not signed")
	// ErrBodyUnsigned is returned when the Body wsu:Id is absent from the
	// verifier's signed-reference set.
	ErrBodyUnsigned = errors.New("ws-security: SOAP Body is not signed")
	// ErrSignatureVerification is returned when the caller's signature
	// verifier rejects the document.
	ErrSignatureVerification = errors.New("ws-security: signature verification failed")
	// ErrUsernameTokenCount is returned when a UsernameToken is required but
	// absent, or when more than one is present.
	ErrUsernameTokenCount = errors.New("ws-security: invalid UsernameToken count")
	// ErrUsernameTokenFields is returned when Nonce or Created is missing,
	// duplicated, or malformed.
	ErrUsernameTokenFields = errors.New("ws-security: invalid UsernameToken fields")
	// ErrNonceReplay is returned when a Nonce/Created pair has already been
	// used within the replay window.
	ErrNonceReplay = errors.New("ws-security: UsernameToken Nonce replayed")
	// ErrReplayStore is returned when the replay store fails.
	ErrReplayStore = errors.New("ws-security: replay store failure")
)

// SignatureVerifier validates the XML signature in the supplied document and
// returns the wsu:Id values listed in the signature's signed references.
//
// The verifier must perform the actual cryptographic and canonicalization
// checks. Returning reference URIs such as "#TS-1" or bare IDs such as "TS-1"
// is supported. The Validator independently requires both the Timestamp and
// Body IDs to be present in the returned set.
type SignatureVerifier func(xmlDoc []byte) (signedReferenceIDs []string, err error)

// Config configures a Validator. VerifySignature is required.
type Config struct {
	VerifySignature SignatureVerifier
	Now             func() time.Time
	ReplayStore     NonceReplayStore

	// AllowMissingUsernameToken disables the default requirement for exactly
	// one UsernameToken. When a UsernameToken is present it is always
	// validated and checked for Nonce replay.
	AllowMissingUsernameToken bool
}

// Validator validates WS-Security headers. Configure its fields before using
// it concurrently. A zero Validator still needs a VerifySignature function.
type Validator struct {
	VerifySignature           SignatureVerifier
	Now                       func() time.Time
	ReplayStore               NonceReplayStore
	AllowMissingUsernameToken bool

	storeOnce sync.Once
}

// Option customizes a Validator created by NewValidator.
type Option func(*Validator)

// WithClock replaces the validator's clock. It is primarily useful for tests.
func WithClock(now func() time.Time) Option {
	return func(validator *Validator) {
		if now != nil {
			validator.Now = now
		}
	}
}

// WithReplayStore replaces the default in-memory replay store.
func WithReplayStore(store NonceReplayStore) Option {
	return func(validator *Validator) {
		if store != nil {
			validator.ReplayStore = store
		}
	}
}

// WithOptionalUsernameToken allows messages without a UsernameToken. A
// UsernameToken that is present is still fully validated.
func WithOptionalUsernameToken() Option {
	return func(validator *Validator) {
		validator.AllowMissingUsernameToken = true
	}
}

// NewValidator creates a Validator with a default in-memory replay store,
// time.Now clock, and a required UsernameToken.
func NewValidator(verify SignatureVerifier, options ...Option) *Validator {
	validator := &Validator{
		VerifySignature: verify,
		Now:             time.Now,
		ReplayStore:     NewMemoryNonceReplayStore(),
	}
	for _, option := range options {
		option(validator)
	}
	return validator
}

// NewValidatorWithConfig creates a Validator from cfg.
func NewValidatorWithConfig(cfg Config) *Validator {
	validator := &Validator{
		VerifySignature:           cfg.VerifySignature,
		Now:                       cfg.Now,
		ReplayStore:               cfg.ReplayStore,
		AllowMissingUsernameToken: cfg.AllowMissingUsernameToken,
	}
	if validator.Now == nil {
		validator.Now = time.Now
	}
	if validator.ReplayStore == nil {
		validator.ReplayStore = NewMemoryNonceReplayStore()
	}
	return validator
}

// Result contains the values validated from the Security header.
type Result struct {
	TimestampID string
	BodyID      string
	Created     time.Time
	Expires     time.Time

	Username             string
	Nonce                string
	UsernameTokenCreated time.Time
}

// ValidationResult is an alias for Result.
type ValidationResult = Result

// Validate validates a complete SOAP message.
func (validator *Validator) Validate(xmlDoc []byte) (*Result, error) {
	if validator.VerifySignature == nil {
		return nil, ErrNoSignatureVerifier
	}

	root, err := parseXMLTree(xmlDoc)
	if err != nil {
		return nil, err
	}

	security, body, err := findSecurityAndBody(root)
	if err != nil {
		return nil, err
	}

	timestamp, usernameToken, err := findSecurityChildren(security)
	if err != nil {
		return nil, err
	}

	timestampID, err := requiredWSUID(timestamp)
	if err != nil {
		return nil, fmt.Errorf("%w: Timestamp", err)
	}
	bodyID, err := requiredWSUID(body)
	if err != nil {
		return nil, fmt.Errorf("%w: Body", err)
	}

	if err := checkWSUIDsUnique(root); err != nil {
		return nil, err
	}

	signedIDs, err := validator.verifySignedReferences(xmlDoc)
	if err != nil {
		return nil, err
	}
	if _, ok := signedIDs[timestampID]; !ok {
		return nil, ErrTimestampUnsigned
	}
	if _, ok := signedIDs[bodyID]; !ok {
		return nil, ErrBodyUnsigned
	}

	created, expires, err := parseTimestamp(timestamp)
	if err != nil {
		return nil, err
	}

	now := validator.now()
	if created.After(now.Add(MaxClockSkew)) {
		return nil, ErrTimestampCreatedInFuture
	}
	if !expires.After(created) || expires.Sub(created) > MaxTimestampLifetime {
		return nil, ErrTimestampLifetime
	}
	if !expires.After(now) {
		return nil, ErrTimestampExpired
	}

	result := &Result{
		TimestampID: timestampID,
		BodyID:      bodyID,
		Created:     created,
		Expires:     expires,
	}

	if usernameToken == nil && !validator.AllowMissingUsernameToken {
		return nil, fmt.Errorf("%w: missing UsernameToken", ErrUsernameTokenCount)
	}
	if usernameToken != nil {
		username, nonce, tokenCreated, err := parseUsernameToken(usernameToken)
		if err != nil {
			return nil, err
		}
		if tokenCreated.After(now.Add(MaxClockSkew)) {
			return nil, fmt.Errorf("%w: UsernameToken Created is in the future", ErrUsernameTokenFields)
		}

		expiresAt := expires
		tokenWindowEnd := tokenCreated.Add(MaxTimestampLifetime)
		if tokenWindowEnd.After(expiresAt) {
			expiresAt = tokenWindowEnd
		}

		accepted, err := validator.replayStore().CheckAndStore(nonce, tokenCreated, expiresAt, now)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrReplayStore, err)
		}
		if !accepted {
			return nil, ErrNonceReplay
		}

		result.Username = username
		result.Nonce = nonce
		result.UsernameTokenCreated = tokenCreated
	}

	return result, nil
}

// ValidateSOAP is an alias for Validate.
func (validator *Validator) ValidateSOAP(xmlDoc []byte) (*Result, error) {
	return validator.Validate(xmlDoc)
}

func (validator *Validator) now() time.Time {
	if validator.Now == nil {
		return time.Now()
	}
	return validator.Now()
}

func (validator *Validator) replayStore() NonceReplayStore {
	validator.storeOnce.Do(func() {
		if validator.ReplayStore == nil {
			validator.ReplayStore = NewMemoryNonceReplayStore()
		}
	})
	return validator.ReplayStore
}

func (validator *Validator) verifySignedReferences(xmlDoc []byte) (map[string]struct{}, error) {
	references, err := validator.VerifySignature(xmlDoc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignatureVerification, err)
	}

	signedIDs := make(map[string]struct{}, len(references))
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			continue
		}
		if fragment := strings.LastIndexByte(reference, '#'); fragment >= 0 {
			reference = reference[fragment+1:]
		}
		if reference != "" {
			signedIDs[reference] = struct{}{}
		}
	}
	return signedIDs, nil
}

func findSecurityAndBody(root *xmlNode) (*xmlNode, *xmlNode, error) {
	if root.name.Local != "Envelope" ||
		(root.name.Space != soap11Namespace && root.name.Space != soap12Namespace) {
		return nil, nil, fmt.Errorf("%w: Envelope element or namespace", ErrMalformedSOAP)
	}

	headers := directChildren(root, root.name.Space, "Header")
	bodies := directChildren(root, root.name.Space, "Body")
	if len(headers) > 1 || len(bodies) != 1 {
		return nil, nil, fmt.Errorf("%w: expected at most one Header and exactly one Body", ErrMalformedSOAP)
	}
	if len(headers) == 0 {
		return nil, nil, ErrSecurityHeader
	}

	securityHeaders := directChildren(headers[0], wsseNamespace, "Security")
	if len(securityHeaders) != 1 {
		return nil, nil, fmt.Errorf("%w: expected exactly one Security header", ErrSecurityHeader)
	}
	return securityHeaders[0], bodies[0], nil
}

func findSecurityChildren(security *xmlNode) (*xmlNode, *xmlNode, error) {
	timestamps := descendants(security, wsuNamespace, "Timestamp")
	directTimestamps := directChildren(security, wsuNamespace, "Timestamp")
	if len(timestamps) != 1 || len(directTimestamps) != 1 {
		return nil, nil, ErrTimestampCount
	}

	usernameTokens := descendants(security, wsseNamespace, "UsernameToken")
	directUsernameTokens := directChildren(security, wsseNamespace, "UsernameToken")
	if len(usernameTokens) != len(directUsernameTokens) || len(directUsernameTokens) > 1 {
		return nil, nil, ErrUsernameTokenCount
	}
	if len(directUsernameTokens) == 0 {
		return timestamps[0], nil, nil
	}
	return timestamps[0], directUsernameTokens[0], nil
}

func requiredWSUID(node *xmlNode) (string, error) {
	if countIDAttributes(node) != 1 {
		return "", fmt.Errorf("%w: expected exactly one wsu:Id", ErrWSUID)
	}
	for _, attr := range node.attrs {
		if attr.Name.Space != wsuNamespace || attr.Name.Local != "Id" {
			continue
		}
		if !validWSUID(attr.Value) {
			return "", fmt.Errorf("%w: malformed Id value", ErrWSUID)
		}
		return attr.Value, nil
	}
	return "", fmt.Errorf("%w: missing wsu:Id", ErrWSUID)
}

func validWSUID(id string) bool {
	return id != "" &&
		strings.TrimSpace(id) == id &&
		!strings.ContainsAny(id, " \t\r\n#")
}

func checkWSUIDsUnique(root *xmlNode) error {
	ids := make(map[string]struct{})
	var visit func(*xmlNode) error
	visit = func(node *xmlNode) error {
		for _, attr := range node.attrs {
			if attr.Name.Space != wsuNamespace || attr.Name.Local != "Id" {
				continue
			}
			if !validWSUID(attr.Value) {
				return fmt.Errorf("%w: malformed Id value", ErrWSUID)
			}
			if _, exists := ids[attr.Value]; exists {
				return ErrDuplicateID
			}
			ids[attr.Value] = struct{}{}
		}
		for _, child := range node.children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root)
}

func parseTimestamp(timestamp *xmlNode) (time.Time, time.Time, error) {
	createdNodes := descendants(timestamp, wsuNamespace, "Created")
	expiresNodes := descendants(timestamp, wsuNamespace, "Expires")
	directCreatedNodes := directChildren(timestamp, wsuNamespace, "Created")
	directExpiresNodes := directChildren(timestamp, wsuNamespace, "Expires")
	if len(createdNodes) != 1 || len(expiresNodes) != 1 ||
		len(directCreatedNodes) != 1 || len(directExpiresNodes) != 1 {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: expected one Created and one Expires", ErrTimestampFields)
	}

	createdText, err := leafText(directCreatedNodes[0])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: Created", ErrTimestampFields)
	}
	expiresText, err := leafText(directExpiresNodes[0])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: Expires", ErrTimestampFields)
	}

	created, err := parseWSTime(createdText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: Created", ErrTimestampFields)
	}
	expires, err := parseWSTime(expiresText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: Expires", ErrTimestampFields)
	}
	return created, expires, nil
}

func parseUsernameToken(token *xmlNode) (string, string, time.Time, error) {
	usernames := descendants(token, wsseNamespace, "Username")
	nonces := descendants(token, wsseNamespace, "Nonce")
	createdNodes := descendants(token, wsuNamespace, "Created")
	directUsernames := directChildren(token, wsseNamespace, "Username")
	directNonces := directChildren(token, wsseNamespace, "Nonce")
	directCreatedNodes := directChildren(token, wsuNamespace, "Created")
	if len(usernames) != len(directUsernames) || len(directUsernames) > 1 ||
		len(nonces) != 1 || len(directNonces) != 1 ||
		len(createdNodes) != 1 || len(directCreatedNodes) != 1 {
		return "", "", time.Time{}, fmt.Errorf("%w: expected at most one Username, and exactly one Nonce and Created", ErrUsernameTokenFields)
	}

	username := ""
	if len(directUsernames) == 1 {
		value, err := leafText(directUsernames[0])
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("%w: Username", ErrUsernameTokenFields)
		}
		username = value
	}

	nonce, err := leafText(directNonces[0])
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w: Nonce", ErrUsernameTokenFields)
	}
	createdText, err := leafText(directCreatedNodes[0])
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w: Created", ErrUsernameTokenFields)
	}
	created, err := parseWSTime(createdText)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("%w: Created", ErrUsernameTokenFields)
	}
	return username, nonce, created, nil
}

func parseWSTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}
