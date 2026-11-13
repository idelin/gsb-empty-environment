package netsec

import (
	"errors"
	"fmt"
)

const (
	// MinSecretLength is the smallest secret length searched by the default
	// attack: one byte.
	MinSecretLength = 1
	// MaxSecretLength is the largest secret length searched by the default
	// attack: 64 bytes.
	MaxSecretLength = 64
)

var (
	// ErrInvalidSecretLength reports a secret length outside [1, 64].
	ErrInvalidSecretLength = errors.New("netsec: secret length must be between 1 and 64")
	// ErrInvalidSecretRange reports an invalid [min, max] secret-length range.
	ErrInvalidSecretRange = errors.New("netsec: invalid secret length range")
	// ErrNilVerifier reports a missing verification callback.
	ErrNilVerifier = errors.New("netsec: verification callback is nil")
	// ErrRangeMiss reports that none of the guessed secret lengths was
	// accepted.  The accompanying AttackResult still contains the query count
	// and the searched range.
	ErrRangeMiss = errors.New("netsec: secret length range was not matched")
)

// SecretLengthRange is an inclusive range of candidate secret lengths.
type SecretLengthRange struct {
	Min int
	Max int
}

// DefaultSecretLengthRange returns the range required by the laboratory
// exercise: 1 through 64 bytes, inclusive.
func DefaultSecretLengthRange() SecretLengthRange {
	return SecretLengthRange{Min: MinSecretLength, Max: MaxSecretLength}
}

// Count returns the number of candidate lengths, or zero for an invalid range.
func (r SecretLengthRange) Count() int {
	if r.Min > r.Max {
		return 0
	}
	return r.Max - r.Min + 1
}

// Contains reports whether length is in the range.
func (r SecretLengthRange) Contains(length int) bool {
	return length >= r.Min && length <= r.Max
}

func (r SecretLengthRange) validate() error {
	if r.Min < MinSecretLength || r.Max > MaxSecretLength || r.Min > r.Max {
		return fmt.Errorf("%w: [%d, %d], want an inclusive subrange of [%d, %d]",
			ErrInvalidSecretRange, r.Min, r.Max, MinSecretLength, MaxSecretLength)
	}
	return nil
}

// LengthExtensionWitness is the complete record of one forged prefix-MAC
// candidate.  For a successful attack it contains every input and output needed
// to reproduce and verify the forgery.
type LengthExtensionWitness struct {
	// SecretLength is the guessed (and, on success, accepted) secret length.
	SecretLength int
	// OriginalMessage and OriginalTag are the captured message and valid tag.
	OriginalMessage []byte
	OriginalTag     []byte
	// Suffix is the attacker-controlled appended field.
	Suffix []byte
	// Padding is the original SHA-256 padding for secret||message.
	Padding []byte
	// ForgedMessage is OriginalMessage || Padding || Suffix.
	ForgedMessage []byte
	// ForgedTag is SHA-256(secret || ForgedMessage), derived by continuing
	// from OriginalTag's chaining state.
	ForgedTag []byte

	// OriginalLength is len(secret) + len(OriginalMessage), the unpadded byte
	// length whose bit length appears in Padding.
	OriginalLength uint64
	// PaddedPrefixLength is the length of secret||message plus Padding.
	PaddedPrefixLength uint64
	// ForgedTotalLength is the number of bytes hashed for ForgedTag, including
	// the secret, forged message, and final SHA-256 padding.
	ForgedTotalLength uint64
	// ForgedBitLength is ForgedTotalLength times eight, modulo 2^64.
	ForgedBitLength uint64
	// QueryNumber is the one-based oracle query that accepted this witness, or
	// zero when the witness was produced without querying an oracle.
	QueryNumber int
}

// AttackResult records an attempted secret-length search.
type AttackResult struct {
	Range   SecretLengthRange
	Queries int
	Witness *LengthExtensionWitness
}

// Found reports whether a candidate was accepted.
func (result AttackResult) Found() bool {
	return result.Witness != nil
}

// RangeMissed reports whether the attack searched all candidates without
// finding an accepted one.
func (result AttackResult) RangeMissed() bool {
	return result.Witness == nil && result.Queries > 0 && result.Queries == result.Range.Count()
}

// MACVerifyFunc is the only oracle interface used by the attack.  It receives a
// candidate message and tag and returns whether the server accepted them.  The
// secret remains inside the server; the attack never receives it.
type MACVerifyFunc func(message, tag []byte) bool

// ForgeSHA256PrefixMAC constructs one length-extension candidate for a known
// message/tag pair and a guessed secret length.
//
// The returned witness has:
//
//	ForgedMessage = message || SHA256Padding(secretLen+len(message)) || suffix
//	ForgedTag     = SHA-256(secret || ForgedMessage)
//
// when the guess is correct.  The tag is obtained by importing the eight
// big-endian state words from tag and compressing suffix and fresh final
// padding.  No SHA-256 collision or preimage is involved.
func ForgeSHA256PrefixMAC(message, tag, suffix []byte, secretLen int) (*LengthExtensionWitness, error) {
	if secretLen < MinSecretLength || secretLen > MaxSecretLength {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidSecretLength, secretLen)
	}

	originalLength := uint64(secretLen) + uint64(len(message))
	forgedTag, padding, err := ExtendSHA256(tag, suffix, originalLength)
	if err != nil {
		return nil, err
	}
	paddedPrefixLength := originalLength + uint64(len(padding))
	forgedTotalLength := paddedPrefixLength + uint64(len(suffix))

	forgedMessage := make([]byte, 0, len(message)+len(padding)+len(suffix))
	forgedMessage = append(forgedMessage, message...)
	forgedMessage = append(forgedMessage, padding...)
	forgedMessage = append(forgedMessage, suffix...)

	return &LengthExtensionWitness{
		SecretLength:       secretLen,
		OriginalMessage:    cloneBytes(message),
		OriginalTag:        cloneBytes(tag),
		Suffix:             cloneBytes(suffix),
		Padding:            cloneBytes(padding),
		ForgedMessage:      cloneBytes(forgedMessage),
		ForgedTag:          forgedTag,
		OriginalLength:     originalLength,
		PaddedPrefixLength: paddedPrefixLength,
		ForgedTotalLength:  forgedTotalLength,
		ForgedBitLength:    forgedTotalLength << 3,
	}, nil
}

// ForgeSHA256LengthExtension is the two-output convenience form of
// ForgeSHA256PrefixMAC.
func ForgeSHA256LengthExtension(message, tag, suffix []byte, secretLen int) (forgedMessage, forgedTag []byte, err error) {
	witness, err := ForgeSHA256PrefixMAC(message, tag, suffix, secretLen)
	if err != nil {
		return nil, nil, err
	}
	return witness.ForgedMessage, witness.ForgedTag, nil
}

// AttackSHA256PrefixMAC searches the default secret-length range [1, 64] in
// ascending order and stops at the first accepted candidate.
//
// It returns ErrRangeMiss (together with a result containing the full query
// count) if the oracle rejects every candidate.
func AttackSHA256PrefixMAC(message, tag, suffix []byte, verify MACVerifyFunc) (AttackResult, error) {
	return AttackSHA256PrefixMACInRange(message, tag, suffix,
		MinSecretLength, MaxSecretLength, verify)
}

// AttackSHA256PrefixMACInRange searches the inclusive secret-length range
// [minSecretLen, maxSecretLen] in ascending order and stops at the first
// accepted candidate.
//
// The verifier callback receives only (message, tag).  The attack has no access
// to the server's secret.  On success the result contains the number of oracle
// queries and a complete witness.  If all candidates fail, it returns
// ErrRangeMiss; use errors.Is to distinguish that expected outcome from
// invalid-input errors.
func AttackSHA256PrefixMACInRange(message, tag, suffix []byte, minSecretLen, maxSecretLen int, verify MACVerifyFunc) (AttackResult, error) {
	secretRange := SecretLengthRange{Min: minSecretLen, Max: maxSecretLen}
	result := AttackResult{Range: secretRange}

	if err := secretRange.validate(); err != nil {
		return result, err
	}
	if verify == nil {
		return result, ErrNilVerifier
	}
	if len(tag) != SHA256TagSize {
		return result, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidTagSize, len(tag), SHA256TagSize)
	}

	for secretLen := minSecretLen; secretLen <= maxSecretLen; secretLen++ {
		witness, err := ForgeSHA256PrefixMAC(message, tag, suffix, secretLen)
		if err != nil {
			return result, err
		}

		result.Queries++
		witness.QueryNumber = result.Queries
		if verify(witness.ForgedMessage, witness.ForgedTag) {
			result.Witness = witness
			return result, nil
		}
	}

	return result, ErrRangeMiss
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}
