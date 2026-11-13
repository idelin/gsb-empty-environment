package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Mode selects the hardened protocol or the deliberately weak review target.
type Mode uint8

const (
	// ModeHardened binds the fixed domain label, session ID, both identity
	// names and public keys, X, and Y into the signed transcript and KDF salt.
	ModeHardened Mode = iota
	// ModeWeak is the deliberately vulnerable review target: signatures cover
	// only X and Y, and the KDF salt contains no identity or session binding.
	ModeWeak
)

// HardenedDomainLabel is the fixed domain separation label for the hardened
// transcript. It is part of every hardened signature and KDF salt.
const HardenedDomainLabel = "example.org/netsec/q0075/p256-ed25519/hardened/v1"

// IsValid reports whether m is a known mode.
func (m Mode) IsValid() bool { return m == ModeHardened || m == ModeWeak }

func (m Mode) String() string {
	switch m {
	case ModeHardened:
		return "hardened"
	case ModeWeak:
		return "weak"
	default:
		return fmt.Sprintf("mode(%d)", uint8(m))
	}
}

// ErrInvalidTranscript is returned when transcript encoding is ambiguous or a
// field is invalid.
var ErrInvalidTranscript = errors.New("netsec: invalid transcript")

// Transcript contains the fields that may be bound by a handshake. In hardened
// mode every field is encoded; in weak mode Encode deliberately emits only X and
// Y, exactly matching the weak signature and KDF coverage.
type Transcript struct {
	Mode      Mode
	SessionID []byte
	Initiator Identity
	Responder Identity
	X         []byte
	Y         []byte
}

// Encode serializes the transcript with 4-byte big-endian length prefixes. X
// and Y are canonicalized to compressed SEC1 form first.
func (t Transcript) Encode() ([]byte, error) {
	if !t.Mode.IsValid() {
		return nil, fmt.Errorf("%w: unknown mode %v", ErrInvalidTranscript, t.Mode)
	}
	_, _, x, err := ParseP256Point(t.X)
	if err != nil {
		return nil, fmt.Errorf("%w: X: %v", ErrInvalidTranscript, err)
	}
	_, _, y, err := ParseP256Point(t.Y)
	if err != nil {
		return nil, fmt.Errorf("%w: Y: %v", ErrInvalidTranscript, err)
	}

	var out []byte
	if t.Mode == ModeWeak {
		out = appendLengthPrefixed(out, x)
		out = appendLengthPrefixed(out, y)
		return out, nil
	}

	if err := validateTranscriptIdentity(t.Initiator); err != nil {
		return nil, fmt.Errorf("%w: initiator: %v", ErrInvalidTranscript, err)
	}
	if err := validateTranscriptIdentity(t.Responder); err != nil {
		return nil, fmt.Errorf("%w: responder: %v", ErrInvalidTranscript, err)
	}
	if err := ValidateSessionID(t.SessionID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTranscript, err)
	}
	out = appendLengthPrefixed(out, []byte(HardenedDomainLabel))
	out = appendLengthPrefixed(out, t.SessionID)
	out = appendLengthPrefixed(out, []byte(t.Initiator.Name))
	out = appendLengthPrefixed(out, t.Initiator.PublicKey)
	out = appendLengthPrefixed(out, []byte(t.Responder.Name))
	out = appendLengthPrefixed(out, t.Responder.PublicKey)
	out = appendLengthPrefixed(out, x)
	out = appendLengthPrefixed(out, y)
	return out, nil
}

// Digest returns SHA-256(Encode()). This is the value signed by both peers and
// used as the HKDF salt.
func (t Transcript) Digest() ([32]byte, error) {
	encoded, err := t.Encode()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func validateTranscriptIdentity(id Identity) error {
	if id.Name == "" || !utf8.ValidString(id.Name) {
		return ErrInvalidIdentityName
	}
	if len(id.PublicKey) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	return nil
}

func appendLengthPrefixed(dst, value []byte) []byte {
	if uint64(len(value)) > uint64(^uint32(0)) {
		panic("netsec: length-prefixed field too large")
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

// HardenedTranscriptDigest is a convenience for computing the hardened digest.
func HardenedTranscriptDigest(sessionID []byte, initiator, responder Identity, x, y []byte) ([32]byte, error) {
	return Transcript{
		Mode: ModeHardened, SessionID: sessionID,
		Initiator: initiator, Responder: responder,
		X: x, Y: y,
	}.Digest()
}

// WeakTranscriptDigest is a convenience for computing the weak digest.
func WeakTranscriptDigest(x, y []byte) ([32]byte, error) {
	return Transcript{Mode: ModeWeak, X: x, Y: y}.Digest()
}

// EqualTranscript reports byte equality of two encoded transcripts. It is
// mostly useful to tests and diagnostics.
func EqualTranscript(a, b Transcript) (bool, error) {
	ae, err := a.Encode()
	if err != nil {
		return false, err
	}
	be, err := b.Encode()
	if err != nil {
		return false, err
	}
	return bytes.Equal(ae, be), nil
}
