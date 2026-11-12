package netsec

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	// MinChallengeLength is the smallest CHAP challenge accepted by a target.
	MinChallengeLength = 16
	// MaxChallengeLength is the largest CHAP challenge accepted by a target.
	MaxChallengeLength = 1024
	// DefaultChallengeLength is used when Config.ChallengeLength is zero.
	DefaultChallengeLength = 32
)

var (
	ErrInvalidConfig          = errors.New("invalid CHAP configuration")
	ErrSameSecrets            = errors.New("CHAP initiator and target secrets must differ")
	ErrEmptySecret            = errors.New("CHAP secrets must not be empty")
	ErrInvalidChallengeLength = errors.New("CHAP challenge length must be between 16 and 1024 bytes")
	ErrChallengeGeneration    = errors.New("could not generate a unique CHAP challenge")
	ErrReflectedChallenge     = errors.New("CHAP challenge was issued by this target")
	ErrInvalidEncoding        = errors.New("invalid CHAP 0x/base64 encoding")
	ErrInvalidResponseLength  = errors.New("CHAP response must decode to 16 bytes")
	ErrInvalidResponse        = errors.New("CHAP response does not match")
	ErrIdentifierMismatch     = errors.New("CHAP identifier does not match the issued challenge")
	ErrUnexpectedState        = errors.New("CHAP session is not in the expected state")
)

// Config describes a target-side mutual CHAP configuration.
//
// InitiatorSecret is the forward secret used to verify the initiator.
// TargetSecret is the reverse secret used when the target answers an
// initiator's challenge. The two secrets must be different.
type Config struct {
	InitiatorSecret string
	TargetSecret    string
	ChallengeLength int
}

// Challenge is a CHAP challenge sent by the target.
type Challenge struct {
	Identifier byte
	Data       []byte
}

// Bytes returns a copy of the challenge octets.
func (c Challenge) Bytes() []byte {
	return append([]byte(nil), c.Data...)
}

// Hex returns the challenge in 0x-prefixed hexadecimal form.
func (c Challenge) Hex() string {
	return EncodeHex(c.Data)
}

// Base64 returns the challenge in standard base64 form.
func (c Challenge) Base64() string {
	return base64.StdEncoding.EncodeToString(c.Data)
}

// Response is a CHAP response produced by this target.
type Response struct {
	Identifier byte
	Data       []byte
}

// Bytes returns a copy of the response octets.
func (r Response) Bytes() []byte {
	return append([]byte(nil), r.Data...)
}

// Hex returns the response in 0x-prefixed hexadecimal form.
func (r Response) Hex() string {
	return EncodeHex(r.Data)
}

// Base64 returns the response in standard base64 form.
func (r Response) Base64() string {
	return base64.StdEncoding.EncodeToString(r.Data)
}

// Authenticator owns target-wide CHAP state. Its challenge registry is shared
// by all connections so a challenge issued on one connection cannot be
// reflected on another.
type Authenticator struct {
	initiatorSecret []byte
	targetSecret    []byte
	challengeLength int

	mu     sync.Mutex
	issued map[string]struct{}
}

// NewAuthenticator validates cfg and creates a target-side authenticator.
func NewAuthenticator(cfg Config) (*Authenticator, error) {
	if cfg.InitiatorSecret == "" || cfg.TargetSecret == "" {
		return nil, ErrEmptySecret
	}
	if subtle.ConstantTimeCompare([]byte(cfg.InitiatorSecret), []byte(cfg.TargetSecret)) == 1 {
		return nil, ErrSameSecrets
	}

	length := cfg.ChallengeLength
	if length == 0 {
		length = DefaultChallengeLength
	}
	if length < MinChallengeLength || length > MaxChallengeLength {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidChallengeLength, length)
	}

	return &Authenticator{
		initiatorSecret: []byte(cfg.InitiatorSecret),
		targetSecret:    []byte(cfg.TargetSecret),
		challengeLength: length,
		issued:          make(map[string]struct{}),
	}, nil
}

// NewSession creates a per-connection CHAP state machine. Call Close when the
// login connection ends to release its issued-challenge registration.
func (a *Authenticator) NewSession() *Session {
	return &Session{auth: a}
}

type sessionState uint8

const (
	stateNew sessionState = iota
	stateForwardChallengeSent
	stateInitiatorVerified
	stateComplete
	stateClosed
)

// Session is the target-side CHAP state for one login connection. Its methods
// are safe for concurrent calls, although login messages are normally handled
// in sequence. Call Close when the login connection ends.
type Session struct {
	auth *Authenticator

	mu                  sync.Mutex
	state               sessionState
	issuedChallenge     []byte
	issuedIdentifier    byte
	challengeRegistered bool
}

// ChallengeInitiator emits the target's forward challenge. It must be called
// once before any other authentication step.
func (s *Session) ChallengeInitiator() (Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != stateNew {
		return Challenge{}, ErrUnexpectedState
	}

	data, id, err := s.auth.generateChallenge()
	if err != nil {
		return Challenge{}, err
	}

	s.issuedChallenge = data
	s.issuedIdentifier = id
	s.challengeRegistered = true
	s.state = stateForwardChallengeSent

	return Challenge{Identifier: id, Data: append([]byte(nil), data...)}, nil
}

// Start is an alias for ChallengeInitiator.
func (s *Session) Start() (Challenge, error) {
	return s.ChallengeInitiator()
}

// VerifyInitiatorResponse checks the response to ChallengeInitiator. The
// response may be 0x-prefixed hexadecimal or standard base64.
func (s *Session) VerifyInitiatorResponse(identifier byte, encodedResponse string) error {
	response, err := DecodeResponse(encodedResponse)
	if err != nil {
		return err
	}
	return s.verifyInitiatorResponseBytes(identifier, response)
}

// VerifyInitiatorResponseBytes is the raw-octet variant of
// VerifyInitiatorResponse.
func (s *Session) VerifyInitiatorResponseBytes(identifier byte, response []byte) error {
	if len(response) != md5.Size {
		return ErrInvalidResponseLength
	}
	return s.verifyInitiatorResponseBytes(identifier, response)
}

func (s *Session) verifyInitiatorResponseBytes(identifier byte, response []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != stateForwardChallengeSent {
		return ErrUnexpectedState
	}
	if identifier != s.issuedIdentifier {
		return ErrIdentifierMismatch
	}

	expected := ResponseDigest(identifier, s.auth.initiatorSecret, s.issuedChallenge)
	if subtle.ConstantTimeCompare(expected, response) != 1 {
		return ErrInvalidResponse
	}

	s.state = stateInitiatorVerified
	return nil
}

// HandleInitiatorChallenge validates the initiator's reverse challenge and
// returns the target's response. The challenge must be 16 to 1024 bytes and
// must never equal a challenge issued by this target on any live connection.
func (s *Session) HandleInitiatorChallenge(identifier byte, challenge []byte) (Response, error) {
	if len(challenge) < MinChallengeLength || len(challenge) > MaxChallengeLength {
		return Response{}, ErrInvalidChallengeLength
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != stateInitiatorVerified {
		return Response{}, ErrUnexpectedState
	}
	if s.auth.wasIssued(challenge) {
		return Response{}, ErrReflectedChallenge
	}

	expected := ResponseDigest(identifier, s.auth.targetSecret, challenge)
	s.state = stateComplete

	return Response{Identifier: identifier, Data: expected}, nil
}

// HandleEncodedInitiatorChallenge is the encoded variant of
// HandleInitiatorChallenge. The challenge may be 0x-prefixed hexadecimal or
// standard base64.
func (s *Session) HandleEncodedInitiatorChallenge(identifier byte, encodedChallenge string) (Response, error) {
	challenge, err := DecodeChallenge(encodedChallenge)
	if err != nil {
		return Response{}, err
	}
	return s.HandleInitiatorChallenge(identifier, challenge)
}

// Close releases the session's issued-challenge registration. It is safe to
// call more than once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == stateClosed {
		return nil
	}
	s.state = stateClosed
	if s.challengeRegistered {
		s.auth.releaseChallenge(s.issuedChallenge)
		s.challengeRegistered = false
	}
	return nil
}

func (a *Authenticator) generateChallenge() ([]byte, byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for attempt := 0; attempt < 32; attempt++ {
		data := make([]byte, a.challengeLength)
		if _, err := io.ReadFull(rand.Reader, data); err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrChallengeGeneration, err)
		}

		key := string(data)
		if _, exists := a.issued[key]; exists {
			continue
		}

		var idBytes [1]byte
		if _, err := io.ReadFull(rand.Reader, idBytes[:]); err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrChallengeGeneration, err)
		}

		a.issued[key] = struct{}{}
		return data, idBytes[0], nil
	}

	return nil, 0, ErrChallengeGeneration
}

func (a *Authenticator) wasIssued(challenge []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	_, exists := a.issued[string(challenge)]
	return exists
}

func (a *Authenticator) releaseChallenge(challenge []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.issued, string(challenge))
}

// ResponseDigest computes MD5(identifier || secret || challenge).
func ResponseDigest(identifier byte, secret []byte, challenge []byte) []byte {
	h := md5.New()
	_, _ = h.Write([]byte{identifier})
	_, _ = h.Write(secret)
	_, _ = h.Write(challenge)
	return h.Sum(nil)
}

// EncodeHex returns data as 0x-prefixed lowercase hexadecimal.
func EncodeHex(data []byte) string {
	return "0x" + hex.EncodeToString(data)
}

// EncodeBase64 returns data in standard base64 form.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeResponse decodes a 0x-prefixed hexadecimal or standard base64 CHAP
// response and requires the result to be one MD5 digest (16 bytes).
func DecodeResponse(encoded string) ([]byte, error) {
	data, err := decodeEncoded(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) != md5.Size {
		return nil, ErrInvalidResponseLength
	}
	return data, nil
}

// DecodeChallenge decodes a 0x-prefixed hexadecimal or standard base64
// challenge and enforces the target's accepted challenge-size range.
func DecodeChallenge(encoded string) ([]byte, error) {
	data, err := decodeEncoded(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) < MinChallengeLength || len(data) > MaxChallengeLength {
		return nil, ErrInvalidChallengeLength
	}
	return data, nil
}

func decodeEncoded(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, ErrInvalidEncoding
	}

	if strings.HasPrefix(encoded, "0x") || strings.HasPrefix(encoded, "0X") {
		if len(encoded) == 2 {
			return nil, ErrInvalidEncoding
		}
		data, err := hex.DecodeString(encoded[2:])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidEncoding, err)
		}
		return data, nil
	}

	if !validBase64Alphabet(encoded) {
		return nil, ErrInvalidEncoding
	}
	if data, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(encoded); err == nil {
		return data, nil
	}
	return nil, ErrInvalidEncoding
}

func validBase64Alphabet(encoded string) bool {
	for _, r := range encoded {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=':
		default:
			return false
		}
	}
	return true
}
