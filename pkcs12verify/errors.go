package pkcs12verify

import "errors"

// Sentinel errors returned by Verify. Callers can use errors.Is to classify a
// rejection without parsing human-readable messages.
var (
	// ErrMalformed indicates the PFX or a nested ASN.1 structure is not valid
	// for the expected PKCS#12 layout.
	ErrMalformed = errors.New("pkcs12verify: malformed PKCS#12 structure")

	// ErrNonCanonicalDER indicates BER-style or otherwise non-DER encoding
	// (indefinite length, non-minimal length, constructed primitive TLV, ...).
	ErrNonCanonicalDER = errors.New("pkcs12verify: non-canonical DER encoding")

	// ErrTrailingBytes indicates data following the complete PFX TLV.
	ErrTrailingBytes = errors.New("pkcs12verify: trailing bytes after PFX")

	// ErrUnsupportedVersion indicates the PFX version is not 3.
	ErrUnsupportedVersion = errors.New("pkcs12verify: unsupported PFX version")

	// ErrMissingMacData indicates the mandatory MacData integrity element is
	// absent, so the AuthSafe cannot be authenticated.
	ErrMissingMacData = errors.New("pkcs12verify: mandatory MacData is missing")

	// ErrDigestAlgorithm indicates a disallowed, weak (MD5/SHA-1) or unknown
	// MAC / PRF digest algorithm.
	ErrDigestAlgorithm = errors.New("pkcs12verify: disallowed MAC digest algorithm")

	// ErrSaltTooShort indicates a MAC or PBKDF2 salt below the policy minimum.
	ErrSaltTooShort = errors.New("pkcs12verify: salt shorter than policy minimum")

	// ErrIterations indicates an iteration count outside the policy window,
	// covering both weak low counts and CPU-exhaustion high counts.
	ErrIterations = errors.New("pkcs12verify: iteration count outside policy window")

	// ErrIntegrity indicates the constant-time MAC comparison failed, i.e. the
	// AuthSafe bytes have been tampered with or the password is wrong.
	ErrIntegrity = errors.New("pkcs12verify: MAC verification failed")

	// ErrUnencryptedKey indicates a plaintext keyBag (unencrypted private key).
	ErrUnencryptedKey = errors.New("pkcs12verify: unencrypted private key bag is forbidden")

	// ErrWeakCipher indicates a legacy weak algorithm (RC2/RC4/DES/3DES/
	// PKCS#12 PBE / PKCS#5 v1 PBE, ...).
	ErrWeakCipher = errors.New("pkcs12verify: weak encryption algorithm")

	// ErrUnsupportedCipher indicates an algorithm that is not weak-legacy but
	// is not on the PBES2 + AES-256-CBC/GCM allow list.
	ErrUnsupportedCipher = errors.New("pkcs12verify: encryption algorithm not allowed")

	// ErrUnsupportedBag indicates a SafeBag type that is not certBag or
	// pkcs8ShroudedKeyBag, or an unsupported certificate kind.
	ErrUnsupportedBag = errors.New("pkcs12verify: unsupported SafeBag type")
)
