// Package netsec provides correlation and replay protection for SAML 2.0
// service-provider responses.
//
// A Correlator records the ID and expiry of every AuthnRequest sent by the
// service provider. ValidateResponse only accepts a response whose
// InResponseTo value matches a live, unconsumed registration, consumes that
// registration exactly once, and records the IDs of all accepted assertions
// so the same assertion cannot be presented again.
//
// The package deliberately does not verify XML signatures. Callers must
// validate the IdP signature before passing the response to a Correlator.
package netsec

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultClockSkew is the tolerance applied to assertion validity windows.
const DefaultClockSkew = 60 * time.Second

const (
	bearerConfirmation  = "urn:oasis:names:tc:SAML:2.0:cm:bearer"
	abandonedRequestTTL = time.Hour
)

var (
	// ErrInvalidConfig indicates that the correlator configuration is unusable.
	ErrInvalidConfig = errors.New("netsec: invalid correlator configuration")
	// ErrInvalidRequestID indicates an empty AuthnRequest ID.
	ErrInvalidRequestID = errors.New("netsec: invalid AuthnRequest ID")
	// ErrInvalidRequestTTL indicates a non-positive AuthnRequest lifetime.
	ErrInvalidRequestTTL = errors.New("netsec: invalid AuthnRequest lifetime")
	// ErrRequestAlreadyRegistered indicates that an ID is already in use.
	ErrRequestAlreadyRegistered = errors.New("netsec: AuthnRequest ID is already registered")
	// ErrUnknownRequest indicates that InResponseTo matched no registration.
	ErrUnknownRequest = errors.New("netsec: response does not match a pending AuthnRequest")
	// ErrExpiredRequest indicates that the matching registration expired.
	ErrExpiredRequest = errors.New("netsec: AuthnRequest registration has expired")
	// ErrRequestReplayed indicates that the registration was already consumed.
	ErrRequestReplayed = errors.New("netsec: AuthnRequest has already been consumed")
	// ErrIdPInitiatedDisabled indicates an unsolicited response was rejected.
	ErrIdPInitiatedDisabled = errors.New("netsec: IdP-initiated responses are disabled")
	// ErrMalformedResponse indicates that the SAML response could not be parsed.
	ErrMalformedResponse = errors.New("netsec: malformed SAML response")
	// ErrMalformedTimestamp indicates an invalid SAML dateTime value.
	ErrMalformedTimestamp = errors.New("netsec: malformed SAML timestamp")
	// ErrMissingAssertion indicates that the response has no assertion.
	ErrMissingAssertion = errors.New("netsec: response contains no assertion")
	// ErrMissingAssertionID indicates an assertion without an ID.
	ErrMissingAssertionID = errors.New("netsec: assertion is missing its ID")
	// ErrDuplicateAssertionID indicates the same assertion ID twice in one response.
	ErrDuplicateAssertionID = errors.New("netsec: response contains a duplicate assertion ID")
	// ErrAssertionReplayed indicates that an assertion ID was already accepted.
	ErrAssertionReplayed = errors.New("netsec: assertion ID has already been seen")
	// ErrRecipientMismatch indicates a Recipient that is not the ACS URL.
	ErrRecipientMismatch = errors.New("netsec: assertion Recipient does not match the ACS URL")
	// ErrMissingRecipientConfirmation indicates no usable subject confirmation.
	ErrMissingRecipientConfirmation = errors.New("netsec: assertion has no usable subject confirmation")
	// ErrMissingExpiry indicates that no NotOnOrAfter deadline is present.
	ErrMissingExpiry = errors.New("netsec: assertion has no NotOnOrAfter deadline")
	// ErrAssertionExpired indicates that NotOnOrAfter is beyond the clock skew.
	ErrAssertionExpired = errors.New("netsec: assertion NotOnOrAfter deadline has passed")
	// ErrAssertionNotYetValid indicates that NotBefore is beyond the clock skew.
	ErrAssertionNotYetValid = errors.New("netsec: assertion NotBefore is in the future")
	// ErrInResponseToMismatch indicates inconsistent request IDs in a response.
	ErrInResponseToMismatch = errors.New("netsec: assertion InResponseTo does not match the response")
)

// Config controls response validation.
type Config struct {
	// ACSURL is the exact Recipient value required in the assertion's
	// SubjectConfirmationData.
	ACSURL string

	// AllowIdPInitiated permits responses without a Response-level
	// InResponseTo attribute. Such responses still have to pass assertion
	// replay, Recipient, and expiry checks.
	AllowIdPInitiated bool

	// ClockSkew is the tolerance applied to NotBefore and NotOnOrAfter.
	// Zero selects DefaultClockSkew (60 seconds).
	ClockSkew time.Duration
}

// PendingRequest describes an AuthnRequest registered for correlation.
type PendingRequest struct {
	ID        string
	ExpiresAt time.Time
}

// ValidatedAssertion is the replay-relevant part of an accepted assertion.
type ValidatedAssertion struct {
	ID        string
	Issuer    string
	ExpiresAt time.Time
}

// ValidatedResponse describes a response that passed all checks.
type ValidatedResponse struct {
	ResponseID   string
	InResponseTo string
	IdPInitiated bool
	Assertions   []ValidatedAssertion
}

type requestRecord struct {
	expiresAt  time.Time
	consumedAt time.Time
}

// Correlator tracks outstanding AuthnRequests and previously accepted
// assertions. It is safe for concurrent use.
type Correlator struct {
	acsURL            string
	allowIdPInitiated bool
	clockSkew         time.Duration

	mu         sync.Mutex
	requests   map[string]*requestRecord
	assertions map[string]time.Time
}

// New creates a Correlator. ACSURL must be an absolute URL.
func New(cfg Config) (*Correlator, error) {
	acsURL := strings.TrimSpace(cfg.ACSURL)
	if acsURL == "" {
		return nil, fmt.Errorf("%w: ACS URL is empty", ErrInvalidConfig)
	}
	parsed, err := url.Parse(acsURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: ACS URL %q is not absolute", ErrInvalidConfig, cfg.ACSURL)
	}
	if cfg.ClockSkew < 0 {
		return nil, fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}
	skew := cfg.ClockSkew
	if skew == 0 {
		skew = DefaultClockSkew
	}
	return &Correlator{
		acsURL:            acsURL,
		allowIdPInitiated: cfg.AllowIdPInitiated,
		clockSkew:         skew,
		requests:          make(map[string]*requestRecord),
		assertions:        make(map[string]time.Time),
	}, nil
}

// BeginAuthnRequest generates an ID suitable for a SAML AuthnRequest and
// registers it with the supplied lifetime.
func (c *Correlator) BeginAuthnRequest(ttl time.Duration) (PendingRequest, error) {
	if ttl <= 0 {
		return PendingRequest{}, fmt.Errorf("%w: %s", ErrInvalidRequestTTL, ttl)
	}
	c.mu.Lock()
	c.prune(time.Now())
	c.mu.Unlock()

	id, err := newRequestID()
	if err != nil {
		return PendingRequest{}, fmt.Errorf("netsec: generate AuthnRequest ID: %w", err)
	}
	expiresAt := time.Now().Add(ttl)
	if err := c.RegisterRequest(id, expiresAt); err != nil {
		return PendingRequest{}, err
	}
	return PendingRequest{ID: id, ExpiresAt: expiresAt}, nil
}

// RegisterRequest records id as an outstanding AuthnRequest that may be
// correlated until expiresAt. Registering the same ID twice is rejected.
func (c *Correlator) RegisterRequest(id string, expiresAt time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: empty ID", ErrInvalidRequestID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.requests[id]; exists {
		return fmt.Errorf("%w: %q", ErrRequestAlreadyRegistered, id)
	}
	c.requests[id] = &requestRecord{expiresAt: expiresAt}
	return nil
}

// ValidateResponse parses and validates a SAML Response using the current
// time. The response must already have had its XML signature verified.
func (c *Correlator) ValidateResponse(raw []byte) (*ValidatedResponse, error) {
	return c.ValidateResponseAt(raw, time.Now())
}

// ValidateResponseAt validates a SAML Response as of now. Supplying the
// clock explicitly makes expiry and skew behavior deterministic in tests.
func (c *Correlator) ValidateResponseAt(raw []byte, now time.Time) (*ValidatedResponse, error) {
	response, err := parseResponse(raw)
	if err != nil {
		return nil, err
	}
	inResponseTo := strings.TrimSpace(response.InResponseTo)
	if inResponseTo == "" && !c.allowIdPInitiated {
		return nil, ErrIdPInitiatedDisabled
	}
	if len(response.Assertions) == 0 {
		return nil, ErrMissingAssertion
	}

	assertions := make([]ValidatedAssertion, 0, len(response.Assertions))
	seenInResponse := make(map[string]struct{}, len(response.Assertions))

	for _, assertion := range response.Assertions {
		validated, err := c.validateAssertion(assertion, inResponseTo, now)
		if err != nil {
			return nil, err
		}
		key := assertionKey(validated.Issuer, validated.ID)
		if _, duplicate := seenInResponse[key]; duplicate {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateAssertionID, validated.ID)
		}
		seenInResponse[key] = struct{}{}
		assertions = append(assertions, validated)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if inResponseTo != "" {
		record, ok := c.requests[inResponseTo]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownRequest, inResponseTo)
		}
		if !now.Before(record.expiresAt) {
			delete(c.requests, inResponseTo)
			return nil, fmt.Errorf("%w: %q", ErrExpiredRequest, inResponseTo)
		}
		if !record.consumedAt.IsZero() {
			return nil, fmt.Errorf("%w: %q", ErrRequestReplayed, inResponseTo)
		}
	}

	for _, assertion := range assertions {
		if _, replayed := c.assertions[assertionKey(assertion.Issuer, assertion.ID)]; replayed {
			return nil, fmt.Errorf("%w: %q", ErrAssertionReplayed, assertion.ID)
		}
	}

	for _, assertion := range assertions {
		c.assertions[assertionKey(assertion.Issuer, assertion.ID)] = assertion.ExpiresAt.Add(c.clockSkew)
	}
	if inResponseTo != "" {
		c.requests[inResponseTo].consumedAt = now
	}
	c.prune(now)

	return &ValidatedResponse{
		ResponseID:   strings.TrimSpace(response.ID),
		InResponseTo: inResponseTo,
		IdPInitiated: inResponseTo == "",
		Assertions:   assertions,
	}, nil
}

func (c *Correlator) validateAssertion(assertion samlAssertion, responseInResponseTo string, now time.Time) (ValidatedAssertion, error) {
	id := strings.TrimSpace(assertion.ID)
	if id == "" {
		return ValidatedAssertion{}, ErrMissingAssertionID
	}
	issuer := strings.TrimSpace(assertion.Issuer)

	var latestDeadline time.Time
	hasDeadline := false
	if assertion.Conditions != nil {
		if value := strings.TrimSpace(assertion.Conditions.NotBefore); value != "" {
			notBefore, err := parseSAMLTime(value)
			if err != nil {
				return ValidatedAssertion{}, fmt.Errorf("%w: Conditions.NotBefore %q", ErrMalformedTimestamp, value)
			}
			if now.Add(c.clockSkew).Before(notBefore) {
				return ValidatedAssertion{}, fmt.Errorf("%w: %s", ErrAssertionNotYetValid, value)
			}
		}
		if value := strings.TrimSpace(assertion.Conditions.NotOnOrAfter); value != "" {
			deadline, err := parseSAMLTime(value)
			if err != nil {
				return ValidatedAssertion{}, fmt.Errorf("%w: Conditions.NotOnOrAfter %q", ErrMalformedTimestamp, value)
			}
			if now.After(deadline.Add(c.clockSkew)) {
				return ValidatedAssertion{}, fmt.Errorf("%w: %s", ErrAssertionExpired, value)
			}
			if !hasDeadline || deadline.After(latestDeadline) {
				latestDeadline = deadline
			}
			hasDeadline = true
		}
	}

	confirmations := assertion.Subject.Confirmations
	if len(confirmations) == 0 {
		return ValidatedAssertion{}, ErrMissingRecipientConfirmation
	}

	var (
		confirmed            bool
		sawRecipientMismatch bool
		sawExpired           bool
		sawNotYetValid       bool
	)
	for _, confirmation := range confirmations {
		method := strings.TrimSpace(confirmation.Method)
		if method != "" && method != bearerConfirmation {
			continue
		}
		data := confirmation.Data
		if data == nil {
			continue
		}

		recipient := data.Recipient
		if recipient != c.acsURL {
			sawRecipientMismatch = true
			continue
		}

		confirmationInResponseTo := strings.TrimSpace(data.InResponseTo)
		if responseInResponseTo != "" && confirmationInResponseTo != "" && confirmationInResponseTo != responseInResponseTo {
			return ValidatedAssertion{}, fmt.Errorf("%w: response %q, assertion %q", ErrInResponseToMismatch, responseInResponseTo, confirmationInResponseTo)
		}

		if value := strings.TrimSpace(data.NotBefore); value != "" {
			notBefore, err := parseSAMLTime(value)
			if err != nil {
				return ValidatedAssertion{}, fmt.Errorf("%w: SubjectConfirmationData.NotBefore %q", ErrMalformedTimestamp, value)
			}
			if now.Add(c.clockSkew).Before(notBefore) {
				sawNotYetValid = true
				continue
			}
		}

		var deadline time.Time
		if value := strings.TrimSpace(data.NotOnOrAfter); value != "" {
			parsed, err := parseSAMLTime(value)
			if err != nil {
				return ValidatedAssertion{}, fmt.Errorf("%w: SubjectConfirmationData.NotOnOrAfter %q", ErrMalformedTimestamp, value)
			}
			if now.After(parsed.Add(c.clockSkew)) {
				sawExpired = true
				continue
			}
			deadline = parsed
		}

		confirmed = true
		if !deadline.IsZero() && (!hasDeadline || deadline.After(latestDeadline)) {
			latestDeadline = deadline
			hasDeadline = true
		}
	}

	if !confirmed {
		switch {
		case sawRecipientMismatch:
			return ValidatedAssertion{}, fmt.Errorf("%w: expected %q", ErrRecipientMismatch, c.acsURL)
		case sawExpired:
			return ValidatedAssertion{}, ErrAssertionExpired
		case sawNotYetValid:
			return ValidatedAssertion{}, ErrAssertionNotYetValid
		default:
			return ValidatedAssertion{}, ErrMissingRecipientConfirmation
		}
	}
	if !hasDeadline {
		return ValidatedAssertion{}, ErrMissingExpiry
	}

	return ValidatedAssertion{ID: id, Issuer: issuer, ExpiresAt: latestDeadline}, nil
}

func (c *Correlator) prune(now time.Time) {
	for id, record := range c.requests {
		if now.After(record.expiresAt.Add(abandonedRequestTTL)) {
			delete(c.requests, id)
		}
	}
	for key, deadline := range c.assertions {
		if now.After(deadline) {
			delete(c.assertions, key)
		}
	}
}

func newRequestID() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "_" + hex.EncodeToString(entropy[:]), nil
}

func assertionKey(issuer, id string) string {
	return issuer + "\x00" + id
}

func parseSAMLTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

type samlResponse struct {
	XMLName      xml.Name        `xml:"Response"`
	ID           string          `xml:"ID,attr"`
	InResponseTo string          `xml:"InResponseTo,attr"`
	Destination  string          `xml:"Destination,attr"`
	Issuer       string          `xml:"Issuer"`
	Assertions   []samlAssertion `xml:"Assertion"`
}

type samlAssertion struct {
	ID         string          `xml:"ID,attr"`
	Issuer     string          `xml:"Issuer"`
	Subject    samlSubject     `xml:"Subject"`
	Conditions *samlConditions `xml:"Conditions"`
}

type samlSubject struct {
	Confirmations []samlSubjectConfirmation `xml:"SubjectConfirmation"`
}

type samlSubjectConfirmation struct {
	Method string                       `xml:"Method,attr"`
	Data   *samlSubjectConfirmationData `xml:"SubjectConfirmationData"`
}

type samlSubjectConfirmationData struct {
	InResponseTo string `xml:"InResponseTo,attr"`
	Recipient    string `xml:"Recipient,attr"`
	NotBefore    string `xml:"NotBefore,attr"`
	NotOnOrAfter string `xml:"NotOnOrAfter,attr"`
}

type samlConditions struct {
	NotBefore    string `xml:"NotBefore,attr"`
	NotOnOrAfter string `xml:"NotOnOrAfter,attr"`
}

func parseResponse(raw []byte) (*samlResponse, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var response samlResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	if response.XMLName.Local != "Response" {
		return nil, fmt.Errorf("%w: root element is %q", ErrMalformedResponse, response.XMLName.Local)
	}
	return &response, nil
}
