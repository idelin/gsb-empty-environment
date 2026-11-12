// Package netsec implements admission control for signed firmware update
// capsules.
//
// A capsule is fully described by externally supplied length fields, so the
// admission step treats every length as attacker-controlled. Before a payload
// is flashed, Admit verifies that:
//
//   - the outer header length, payload length and authentication header length
//     match fixed layouts and sum to the capsule size without 32/64-bit
//     integer wraparound;
//   - the signature is verified over exactly the bytes that will be flashed
//     (the payload) concatenated with the capsule's monotonic counter, so a
//     length shuffle cannot make the signed region differ from the flashed
//     region;
//   - the capsule counter is strictly greater than the stored counter
//     (anti-replay);
//   - the firmware version is not below the configured minimum supported
//     version (anti-rollback).
//
// The new counter is committed only after the caller reports a successful
// flash via Commit, so a power loss before commit leaves the stored counter
// untouched and the same capsule remains admissible for retry.
package netsec

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"math"
)

// Capsule magic: "FWUP".
const CapsuleMagic uint32 = 0x46575550

// Fixed binary sizes of the capsule sections, in bytes.
const (
	// OuterHeaderSize is the fixed outer header layout.
	OuterHeaderSize = 16
	// AuthHeaderSize is the fixed authentication header layout:
	// counter (8) + version (4) + signature length (4) + signature.
	AuthHeaderSize = 16 + ed25519.SignatureSize
)

// OuterHeader is the unauthenticated outer capsule header. All multi-byte
// fields are big-endian.
type OuterHeader struct {
	Magic      uint32
	HeaderLen  uint32
	PayloadLen uint32
	AuthHdrLen uint32
}

// AuthHeader carries the authenticated update metadata and the detached
// signature over payload || big-endian counter.
type AuthHeader struct {
	Counter   uint64
	Version   uint32
	SigLen    uint32
	Signature []byte
}

// Sentinel errors returned by the admission path. Callers should inspect
// failures with errors.Is rather than matching message text.
var (
	// ErrTruncatedCapsule means the buffer is shorter than its declared
	// sections or shorter than the fixed minimum layout.
	ErrTruncatedCapsule = errors.New("netsec: capsule shorter than declared sections")
	// ErrTrailingBytes means the capsule contains bytes beyond the declared
	// header, payload and authentication header.
	ErrTrailingBytes = errors.New("netsec: capsule has trailing bytes beyond declared length")
	// ErrHeaderLength means the declared outer header length does not match
	// the fixed layout.
	ErrHeaderLength = errors.New("netsec: outer header length mismatch")
	// ErrAuthLength means the declared authentication header length does not
	// match the fixed layout, or the embedded signature length is wrong.
	ErrAuthLength = errors.New("netsec: authentication header length mismatch")
	// ErrLengthOverflow means adding the declared lengths wraps around or
	// exceeds the addressable integer range.
	ErrLengthOverflow = errors.New("netsec: capsule length addition overflows")
	// ErrBadMagic means the outer header magic is not CapsuleMagic.
	ErrBadMagic = errors.New("netsec: bad capsule magic")
	// ErrVersionUnsupported means the firmware version is below the minimum
	// supported version.
	ErrVersionUnsupported = errors.New("netsec: firmware version below minimum supported version")
	// ErrCounterReplay means the capsule counter is not strictly greater than
	// the stored monotonic counter.
	ErrCounterReplay = errors.New("netsec: capsule counter is not greater than stored counter")
	// ErrSignature means the signature does not verify over the exact payload
	// and counter bytes.
	ErrSignature = errors.New("netsec: signature verification failed")
	// ErrInvalidPolicy means the admission policy is not usable.
	ErrInvalidPolicy = errors.New("netsec: invalid admission policy")
)

// SignatureVerifier verifies a detached signature over message. Implementations
// must return a result that wraps ErrSignature on a cryptographic failure so
// callers can classify it with errors.Is.
type SignatureVerifier interface {
	Verify(message, signature []byte) error
}

// ED25519Verifier is a SignatureVerifier backed by an Ed25519 public key.
type ED25519Verifier struct {
	PublicKey ed25519.PublicKey
}

// Verify reports whether signature is a valid Ed25519 signature of message.
func (v ED25519Verifier) Verify(message, signature []byte) error {
	if len(v.PublicKey) != ed25519.PublicKeySize {
		return ErrSignature
	}
	if !ed25519.Verify(v.PublicKey, message, signature) {
		return ErrSignature
	}
	return nil
}

// Policy groups the external inputs needed to admit a capsule.
type Policy struct {
	// Verifier authenticates the capsule signature.
	Verifier SignatureVerifier
	// Counter stores the committed monotonic counter.
	Counter CounterStore
	// MinSupportedVersion is the lowest firmware version that may be flashed.
	MinSupportedVersion uint32
}

// AdmittedCapsule is a capsule that has passed every admission check. Payload
// is a private copy; it is exactly the bytes that will be flashed.
type AdmittedCapsule struct {
	Payload []byte
	Counter uint64
	Version uint32
}

// Admit performs every pre-flash check against capsule and returns the payload
// to flash. The CounterStore is never modified here; Commit persists the new
// counter only after the flash succeeds.
func (p Policy) Admit(ctx context.Context, capsule []byte) (*AdmittedCapsule, error) {
	if p.Verifier == nil || p.Counter == nil {
		return nil, ErrInvalidPolicy
	}
	if len(capsule) < OuterHeaderSize {
		return nil, ErrTruncatedCapsule
	}

	// Outer header fields are attacker-controlled; bounds are re-checked after
	// every addition below.
	hdr := OuterHeader{
		Magic:      binary.BigEndian.Uint32(capsule[0:4]),
		HeaderLen:  binary.BigEndian.Uint32(capsule[4:8]),
		PayloadLen: binary.BigEndian.Uint32(capsule[8:12]),
		AuthHdrLen: binary.BigEndian.Uint32(capsule[12:16]),
	}
	if hdr.Magic != CapsuleMagic {
		return nil, ErrBadMagic
	}
	if hdr.HeaderLen != OuterHeaderSize {
		return nil, ErrHeaderLength
	}
	if hdr.AuthHdrLen != AuthHeaderSize {
		return nil, ErrAuthLength
	}

	// Require every intermediate sum to fit both uint32 (so the on-wire fields
	// cannot wrap) and int (so slice indices cannot wrap on 32-bit targets).
	headerEnd, ok := addChecked64(uint64(hdr.HeaderLen), uint64(hdr.PayloadLen))
	if !ok || headerEnd > uint64(maxInt) {
		return nil, ErrLengthOverflow
	}
	totalLen, ok := addChecked64(headerEnd, uint64(hdr.AuthHdrLen))
	if !ok || totalLen > uint64(maxInt) {
		return nil, ErrLengthOverflow
	}
	// Every wire field is uint32, so a well-formed capsule must itself fit in
	// uint32. Rejecting values whose 32-bit sum would wrap also defeats
	// implementations that naively add the fields in 32-bit arithmetic.
	if totalLen > math.MaxUint32 {
		return nil, ErrLengthOverflow
	}
	if uint64(len(capsule)) < totalLen {
		return nil, ErrTruncatedCapsule
	}
	if uint64(len(capsule)) > totalLen {
		return nil, ErrTrailingBytes
	}

	payloadStart := uint64(hdr.HeaderLen)
	payloadEnd := headerEnd
	authEnd := totalLen
	if authEnd != payloadEnd+uint64(hdr.AuthHdrLen) {
		return nil, ErrLengthOverflow
	}

	auth := capsule[payloadEnd:authEnd]
	parsed := AuthHeader{
		Counter: binary.BigEndian.Uint64(auth[0:8]),
		Version: binary.BigEndian.Uint32(auth[8:12]),
		SigLen:  binary.BigEndian.Uint32(auth[12:16]),
	}
	if parsed.SigLen != ed25519.SignatureSize {
		return nil, ErrAuthLength
	}
	parsed.Signature = auth[16 : 16+ed25519.SignatureSize]

	if parsed.Version < p.MinSupportedVersion {
		return nil, ErrVersionUnsupported
	}

	stored, err := p.Counter.Current(ctx)
	if err != nil {
		return nil, err
	}
	if parsed.Counter <= stored {
		return nil, ErrCounterReplay
	}

	// The signed message is exactly the payload that will be flashed followed
	// by the big-endian counter. Any length tampering changes which payload
	// bytes reach this point, and the signature then fails.
	payload := capsule[payloadStart:payloadEnd]
	signed := makeSignedMessage(payload, parsed.Counter)
	if err := p.Verifier.Verify(signed, parsed.Signature); err != nil {
		return nil, err
	}

	admitted := &AdmittedCapsule{
		Payload: append([]byte(nil), payload...),
		Counter: parsed.Counter,
		Version: parsed.Version,
	}
	return admitted, nil
}

// Commit persists the admitted counter after a successful flash. It must be
// called only once the caller confirms the payload was flashed; committing
// before that would make a failed update irreversible under replay protection.
func (p Policy) Commit(ctx context.Context, admitted *AdmittedCapsule) error {
	if p.Counter == nil || admitted == nil {
		return ErrInvalidPolicy
	}
	return p.Counter.Commit(ctx, admitted.Counter)
}

// makeSignedMessage builds the exact byte string covered by the signature:
// payload || big-endian uint64 counter.
func makeSignedMessage(payload []byte, counter uint64) []byte {
	message := make([]byte, len(payload)+8)
	copy(message, payload)
	binary.BigEndian.PutUint64(message[len(payload):], counter)
	return message
}

// addChecked64 returns a+b and false when the addition wraps uint64.
func addChecked64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}

const maxInt = int(^uint(0) >> 1)
