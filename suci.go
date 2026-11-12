package netsec

import (
	"errors"
	"fmt"
)

// ProtectionScheme is the SUCI protection scheme identifier carried outside
// of the scheme output.
type ProtectionScheme uint8

const (
	// SchemeNull sends the identity in clear text. It is only suitable for
	// emergency registration.
	SchemeNull ProtectionScheme = 0
	// SchemeProfileA is ECIES Profile A (Curve25519).
	SchemeProfileA ProtectionScheme = 1
	// SchemeProfileB is ECIES Profile B (secp256r1).
	SchemeProfileB ProtectionScheme = 2
)

const (
	// ProfileAEphemeralPublicKeyLength is the size of a Curve25519
	// ephemeral public key in a Profile A scheme output.
	ProfileAEphemeralPublicKeyLength = 32
	// ProfileAMACLength is the size of the MAC in a Profile A scheme output.
	ProfileAMACLength = 8

	// ProfileBEphemeralPublicKeyLength is the size of a compressed
	// secp256r1 point in a Profile B scheme output.
	ProfileBEphemeralPublicKeyLength = 33
	// ProfileBMACLength is the size of the MAC in a Profile B scheme output.
	ProfileBMACLength = 8
)

func (s ProtectionScheme) String() string {
	switch s {
	case SchemeNull:
		return "null"
	case SchemeProfileA:
		return "profile-a"
	case SchemeProfileB:
		return "profile-b"
	default:
		return fmt.Sprintf("scheme-%d", uint8(s))
	}
}

// ProfileLayout reports the scheme-output field sizes for a protection
// profile. It returns ok=false for the null scheme and unknown schemes.
func ProfileLayout(scheme ProtectionScheme) (ephemeralKeyLength, macLength int, ok bool) {
	switch scheme {
	case SchemeProfileA:
		return ProfileAEphemeralPublicKeyLength, ProfileAMACLength, true
	case SchemeProfileB:
		return ProfileBEphemeralPublicKeyLength, ProfileBMACLength, true
	default:
		return 0, 0, false
	}
}

// EncryptedPayload is the parsed form of a Profile A or Profile B scheme
// output: ephemeral public key || ciphertext || MAC.
type EncryptedPayload struct {
	EphemeralPublicKey []byte
	Ciphertext         []byte
	MAC                []byte
}

// Request is an inbound SUCI admission request. SchemeOutput is either the
// clear-text identity for the null scheme or the encrypted scheme output for
// a protection profile.
type Request struct {
	RoutingIndicator       string
	ProtectionScheme       ProtectionScheme
	HomeNetworkPublicKeyID int
	EmergencyRegistration  bool
	SchemeOutput           []byte
}

// SUCI is an alias for Request.
type SUCI = Request

// Result is a successfully admitted and de-concealed identity.
type Result struct {
	Scheme                 ProtectionScheme
	RoutingIndicator       string
	HomeNetworkPublicKeyID int
	KeyGeneration          KeyGeneration
	SUPI                   []byte
}

// Decryptor is the injected cryptographic boundary used by Admission.
// VerifyMAC must authenticate the scheme output before Decrypt is called.
type Decryptor interface {
	VerifyMAC(scheme ProtectionScheme, key []byte, payload EncryptedPayload) error
	Decrypt(scheme ProtectionScheme, key []byte, payload EncryptedPayload) ([]byte, error)
}

// DecryptorFuncs adapts functions to a Decryptor.
type DecryptorFuncs struct {
	VerifyMACFunc func(scheme ProtectionScheme, key []byte, payload EncryptedPayload) error
	DecryptFunc   func(scheme ProtectionScheme, key []byte, payload EncryptedPayload) ([]byte, error)
}

func (d DecryptorFuncs) VerifyMAC(scheme ProtectionScheme, key []byte, payload EncryptedPayload) error {
	if d.VerifyMACFunc == nil {
		return fmt.Errorf("%w: VerifyMACFunc is nil", ErrMACVerificationFailed)
	}
	return d.VerifyMACFunc(scheme, key, payload)
}

func (d DecryptorFuncs) Decrypt(scheme ProtectionScheme, key []byte, payload EncryptedPayload) ([]byte, error) {
	if d.DecryptFunc == nil {
		return nil, fmt.Errorf("%w: DecryptFunc is nil", ErrDecryptionFailed)
	}
	return d.DecryptFunc(scheme, key, payload)
}

var (
	// ErrInvalidRoutingIndicator means the routing indicator is not 1 to 4
	// ASCII decimal digits.
	ErrInvalidRoutingIndicator = errors.New("netsec: invalid routing indicator")
	// ErrUnsupportedProtectionScheme means the scheme identifier is not 0, 1,
	// or 2.
	ErrUnsupportedProtectionScheme = errors.New("netsec: unsupported SUCI protection scheme")
	// ErrNullSchemeNotEmergency rejects the null protection scheme unless the
	// caller has marked the registration as an emergency registration.
	ErrNullSchemeNotEmergency = errors.New("netsec: null protection scheme requires emergency registration")
	// ErrDowngrade is an alias for ErrNullSchemeNotEmergency.
	ErrDowngrade = ErrNullSchemeNotEmergency
	// ErrUnknownHomeNetworkKey means the public key identifier is absent from
	// both the current and the previous generation key table.
	ErrUnknownHomeNetworkKey = errors.New("netsec: home network public key identifier not found in key ring")
	// ErrMalformedSchemeOutput means the scheme output cannot be split into
	// ephemeral public key, ciphertext, and MAC.
	ErrMalformedSchemeOutput = errors.New("netsec: malformed SUCI scheme output")
	// ErrMACVerificationFailed means the injected MAC verifier rejected the
	// scheme output.
	ErrMACVerificationFailed = errors.New("netsec: SUCI MAC verification failed")
	// ErrDecryptionFailed means the injected decryptor failed after the MAC
	// had been verified.
	ErrDecryptionFailed = errors.New("netsec: SUCI decryption failed")
	// ErrReplayDetected means the ephemeral public key was already seen inside
	// the configured replay window.
	ErrReplayDetected = errors.New("netsec: SUCI ephemeral public key replayed within replay window")
	// ErrEmptyIdentity means de-concealment produced no identity.
	ErrEmptyIdentity = errors.New("netsec: SUCI resolved to an empty identity")
	// ErrInvalidKeyID means a key table contains an identifier outside the
	// SUCI-defined range 1..255.
	ErrInvalidKeyID = errors.New("netsec: home network public key identifier must be in 1..255")
	// ErrEmptyKeyMaterial means a key table entry has no key material.
	ErrEmptyKeyMaterial = errors.New("netsec: key material must not be empty")
	// ErrNilKeyRing means NewAdmission was called without a key ring.
	ErrNilKeyRing = errors.New("netsec: key ring must not be nil")
	// ErrNilDecryptor means NewAdmission was called without a decryptor.
	ErrNilDecryptor = errors.New("netsec: decryptor must not be nil")
	// ErrInvalidReplayWindow means the replay window is not positive.
	ErrInvalidReplayWindow = errors.New("netsec: replay window must be positive")
	// ErrNilClock means the injected clock is nil.
	ErrNilClock = errors.New("netsec: clock must not be nil")
)

func validateRoutingIndicator(ri string) error {
	if len(ri) < 1 || len(ri) > 4 {
		return fmt.Errorf("%w: %q has length %d", ErrInvalidRoutingIndicator, ri, len(ri))
	}
	for i := 0; i < len(ri); i++ {
		if ri[i] < '0' || ri[i] > '9' {
			return fmt.Errorf("%w: %q is not decimal", ErrInvalidRoutingIndicator, ri)
		}
	}
	return nil
}

func parseEncryptedPayload(scheme ProtectionScheme, output []byte) (EncryptedPayload, error) {
	ephemeralKeyLength, macLength, ok := ProfileLayout(scheme)
	if !ok {
		return EncryptedPayload{}, fmt.Errorf("%w: %s", ErrUnsupportedProtectionScheme, scheme)
	}
	if len(output) <= ephemeralKeyLength+macLength {
		return EncryptedPayload{}, fmt.Errorf(
			"%w: %s output has %d bytes, need more than %d",
			ErrMalformedSchemeOutput, scheme, len(output), ephemeralKeyLength+macLength,
		)
	}
	macStart := len(output) - macLength
	ciphertextEnd := macStart
	ephemeralKeyEnd := ephemeralKeyLength

	return EncryptedPayload{
		EphemeralPublicKey: output[:ephemeralKeyEnd],
		Ciphertext:         output[ephemeralKeyEnd:ciphertextEnd],
		MAC:                output[macStart:],
	}, nil
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
