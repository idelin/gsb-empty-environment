package taskpkg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// Transport identifies the network transport a response arrived on.
type Transport string

const (
	TransportUDP Transport = "udp"
	TransportTCP Transport = "tcp"
)

// ChallengeLen is the byte length of the per-request random challenge.
const ChallengeLen = 32

// Question is the DNS question being resolved in this request round.
type Question struct {
	Name  string
	Type  uint16
	Class uint16
}

// Answer is the complete resolution payload returned by an upstream.
type Answer struct {
	Records []string
}

// Result is the single authenticated outcome of a session.
type Result struct {
	Transport Transport
	Answer    Answer
}

var (
	// ErrBadSignature is returned when a response signature does not cover
	// the bound question, upstream, public key, challenge and full answer.
	ErrBadSignature = errors.New("taskpkg: response signature does not match bound request fields")
	// ErrInvalidPublicKey is returned when the trusted upstream key is malformed.
	ErrInvalidPublicKey = errors.New("taskpkg: invalid ed25519 public key")
)

// signingDomain separates transport-validation signatures from any other
// use of the same upstream key.
const signingDomain = "domainreissue/q024 transport-validation v1"

// Session tracks one request round. It binds the question, the trusted
// upstream, its public key and a fresh random challenge. A response is
// deliverable only if its ed25519 signature covers all of those fields plus
// the complete answer. UDP truncation never yields trusted data; it only
// advises the host to re-query over TCP. UDP and TCP responses may race, but
// exactly one authenticated complete answer is ever published.
type Session struct {
	mu        sync.Mutex
	question  Question
	upstream  string
	publicKey ed25519.PublicKey
	challenge [ChallengeLen]byte

	udpTruncated bool
	delivered    bool
	result       Result
}

// NewSession starts a request round for question against the trusted
// upstream identified by name and publicKey, generating a fresh random
// challenge.
func NewSession(question Question, upstream string, publicKey ed25519.PublicKey) (*Session, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	s := &Session{
		question:  question,
		upstream:  upstream,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
	}
	if _, err := rand.Read(s.challenge[:]); err != nil {
		return nil, fmt.Errorf("taskpkg: generate challenge: %w", err)
	}
	return s, nil
}

// Challenge returns a copy of this round's random challenge.
func (s *Session) Challenge() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, ChallengeLen)
	copy(out, s.challenge[:])
	return out
}

// SigningPayload returns the canonical byte string the trusted upstream must
// sign for answer in this round. It commits to the question, the upstream
// identity, the upstream public key, the random challenge and the full
// answer.
func (s *Session) SigningPayload(answer Answer) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signingPayloadLocked(answer)
}

func (s *Session) signingPayloadLocked(answer Answer) []byte {
	var buf bytes.Buffer
	writeBytes(&buf, []byte(signingDomain))
	writeString(&buf, s.question.Name)
	binary.Write(&buf, binary.BigEndian, s.question.Type)
	binary.Write(&buf, binary.BigEndian, s.question.Class)
	writeString(&buf, s.upstream)
	writeBytes(&buf, s.publicKey)
	buf.Write(s.challenge[:])
	binary.Write(&buf, binary.BigEndian, uint32(len(answer.Records)))
	for _, record := range answer.Records {
		writeString(&buf, record)
	}
	return buf.Bytes()
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	binary.Write(buf, binary.BigEndian, uint32(len(b)))
	buf.Write(b)
}

func writeString(buf *bytes.Buffer, v string) {
	writeBytes(buf, []byte(v))
}

// Submit verifies a candidate response that arrived on transport. The
// signature must be a valid ed25519 signature by the bound public key over
// SigningPayload(answer). The first valid response is published as the
// session result; later responses, valid or not, never replace it. It
// reports whether this submission was the one published.
func (s *Session) Submit(transport Transport, answer Answer, signature []byte) (Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ed25519.Verify(s.publicKey, s.signingPayloadLocked(answer), signature) {
		return Result{}, false, ErrBadSignature
	}
	if s.delivered {
		return s.result, false, nil
	}
	s.delivered = true
	s.result = Result{Transport: transport, Answer: answer}
	return s.result, true, nil
}

// NoteTruncated records that a response on transport arrived truncated. A
// truncated UDP response carries no trusted data: it only signals the host
// to re-issue the query over TCP. It reports whether a TCP retry is advised.
func (s *Session) NoteTruncated(transport Transport) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if transport == TransportUDP {
		s.udpTruncated = true
	}
	return !s.delivered
}

// UDPTruncated reports whether a truncated UDP response has been observed.
func (s *Session) UDPTruncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udpTruncated
}

// Result returns the published authenticated result, if any.
func (s *Session) Result() (Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.delivered
}
