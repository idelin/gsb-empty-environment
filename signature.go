package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
)

// SignatureScheme is a TLS SignatureScheme value.
type SignatureScheme uint16

// TLS 1.3 signature schemes that may be used with CertificateVerify. The
// legacy PKCS#1 v1.5 schemes are included for callers that also handle older
// protocol versions; TLS 1.3 CertificateVerify uses the RSA-PSS schemes.
const (
	SchemeUnknown SignatureScheme = 0

	PKCS1WithSHA1   SignatureScheme = 0x0201
	PKCS1WithSHA256 SignatureScheme = 0x0401
	PKCS1WithSHA384 SignatureScheme = 0x0501
	PKCS1WithSHA512 SignatureScheme = 0x0601

	ECDSAWithP256AndSHA256 SignatureScheme = 0x0403
	ECDSAWithP384AndSHA384 SignatureScheme = 0x0503
	ECDSAWithP521AndSHA512 SignatureScheme = 0x0603

	PSSWithSHA256 SignatureScheme = 0x0804
	PSSWithSHA384 SignatureScheme = 0x0805
	PSSWithSHA512 SignatureScheme = 0x0806

	Ed25519 SignatureScheme = 0x0807
)

// Hash returns the hash associated with scheme. Ed25519 returns zero because
// it hashes the message internally.
func (scheme SignatureScheme) Hash() crypto.Hash {
	switch scheme {
	case PKCS1WithSHA1:
		return crypto.SHA1
	case PKCS1WithSHA256, ECDSAWithP256AndSHA256, PSSWithSHA256:
		return crypto.SHA256
	case PKCS1WithSHA384, ECDSAWithP384AndSHA384, PSSWithSHA384:
		return crypto.SHA384
	case PKCS1WithSHA512, ECDSAWithP521AndSHA512, PSSWithSHA512:
		return crypto.SHA512
	default:
		return 0
	}
}

func (scheme SignatureScheme) isPSS() bool {
	return scheme == PSSWithSHA256 || scheme == PSSWithSHA384 || scheme == PSSWithSHA512
}

func (scheme SignatureScheme) isPKCS1() bool {
	return scheme == PKCS1WithSHA1 || scheme == PKCS1WithSHA256 ||
		scheme == PKCS1WithSHA384 || scheme == PKCS1WithSHA512
}

func (scheme SignatureScheme) isECDSA() bool {
	return scheme == ECDSAWithP256AndSHA256 ||
		scheme == ECDSAWithP384AndSHA384 ||
		scheme == ECDSAWithP521AndSHA512
}

func (scheme SignatureScheme) isRSA() bool {
	return scheme.isPSS() || scheme.isPKCS1()
}

// String returns the TLS name of scheme.
func (scheme SignatureScheme) String() string {
	switch scheme {
	case PKCS1WithSHA1:
		return "rsa_pkcs1_sha1"
	case PKCS1WithSHA256:
		return "rsa_pkcs1_sha256"
	case PKCS1WithSHA384:
		return "rsa_pkcs1_sha384"
	case PKCS1WithSHA512:
		return "rsa_pkcs1_sha512"
	case ECDSAWithP256AndSHA256:
		return "ecdsa_secp256r1_sha256"
	case ECDSAWithP384AndSHA384:
		return "ecdsa_secp384r1_sha384"
	case ECDSAWithP521AndSHA512:
		return "ecdsa_secp521r1_sha512"
	case PSSWithSHA256:
		return "rsa_pss_rsae_sha256"
	case PSSWithSHA384:
		return "rsa_pss_rsae_sha384"
	case PSSWithSHA512:
		return "rsa_pss_rsae_sha512"
	case Ed25519:
		return "ed25519"
	default:
		return fmt.Sprintf("SignatureScheme(0x%04x)", uint16(scheme))
	}
}

// SignOptions controls SignCertificateVerify and SignSignedMessage.
type SignOptions struct {
	// Hash selects the digest. If zero, the hash is inferred from Scheme or
	// from the private key (SHA-256 by default).
	Hash crypto.Hash

	// Scheme selects the TLS signature algorithm. If zero, RSA uses PSS and
	// ECDSA uses the curve's natural hash.
	Scheme SignatureScheme

	// PSSSaltLength is the RSA-PSS salt length. Zero uses the TLS 1.3
	// requirement of a salt as long as the hash output.
	PSSSaltLength int

	// Rand supplies randomness for signing. If nil, crypto/rand.Reader is
	// used.
	Rand io.Reader
}

// VerifyOptions controls VerifyCertificateVerify, VerifyContext,
// VerifySignedMessage, and VerifySignature.
type VerifyOptions struct {
	// Hash selects the digest. If zero, it is inferred from Scheme or from
	// the public key.
	Hash crypto.Hash

	// Scheme selects the TLS signature algorithm. If zero, the algorithm is
	// inferred from the public key; RSA defaults to PSS.
	Scheme SignatureScheme

	// PSSSaltLength requires a particular RSA-PSS salt length when non-zero.
	PSSSaltLength int

	// RequirePSSSaltLengthEqualsHash enforces the TLS 1.3 requirement that
	// the PSS salt length equal the digest length. It is overridden by a
	// non-zero PSSSaltLength.
	RequirePSSSaltLengthEqualsHash bool
}

var (
	// ErrInvalidSignature reports a signature that did not verify.
	ErrInvalidSignature = errors.New("netsec: CertificateVerify signature verification failed")

	// ErrUnsupportedKey reports a public or private key type that this
	// package cannot use.
	ErrUnsupportedKey = errors.New("netsec: unsupported key type")

	// ErrUnsupportedHash reports an unavailable or unsuitable digest.
	ErrUnsupportedHash = errors.New("netsec: unsupported hash")

	// ErrUnsupportedScheme reports a signature scheme that is unknown or
	// incompatible with the supplied key.
	ErrUnsupportedScheme = errors.New("netsec: unsupported signature scheme")
)

// VerifyCertificateVerify verifies signature over the RFC 8446
// CertificateVerify input for role and transcriptHash.
//
// The context prefix is constructed internally, so a signature made over a
// shortened prefix cannot be accepted.
func VerifyCertificateVerify(pub crypto.PublicKey, role Role, transcriptHash, signature []byte, opts *VerifyOptions) error {
	message, err := SignedMessage(role, transcriptHash)
	if err != nil {
		return err
	}
	return VerifySignedMessage(pub, message, signature, opts)
}

// VerifyContext verifies signature using context verbatim. context must be the
// complete RFC 8446 context, including all 64 leading 0x20 octets. A context
// with 63 spaces is rejected before any cryptographic operation.
func VerifyContext(pub crypto.PublicKey, context, transcriptHash, signature []byte, opts *VerifyOptions) error {
	if err := ValidateContext(context); err != nil {
		return err
	}
	message := make([]byte, 0, len(context)+1+len(transcriptHash))
	message = append(message, context...)
	message = append(message, 0)
	message = append(message, transcriptHash...)
	return VerifySignedMessage(pub, message, signature, opts)
}

// VerifySignedMessage verifies signature over a complete
// CertificateVerify signed message. The message must contain exactly 64
// leading spaces followed by a valid server or client context label, a 0x00
// separator, and the transcript hash.
func VerifySignedMessage(pub crypto.PublicKey, message, signature []byte, opts *VerifyOptions) error {
	if err := ValidateSignedMessage(message); err != nil {
		return err
	}
	return VerifySignature(pub, 0, message, signature, opts)
}

// VerifySignature verifies signature over message using the TLS signature
// algorithm selected by opts. If opts.Scheme is zero, the algorithm is
// inferred from pub (RSA defaults to PSS). The hash argument, when non-zero,
// is used as a default for opts.Hash.
//
// This is a lower-level helper: unlike VerifySignedMessage, it does not
// inspect the TLS CertificateVerify context.
func VerifySignature(pub crypto.PublicKey, hash crypto.Hash, message, signature []byte, opts *VerifyOptions) error {
	pub = normalizePublicKey(pub)
	options := normalizeVerifyOptions(hash, opts)
	scheme, digestHash, err := resolveSchemeAndHash(pub, &options)
	if err != nil {
		return err
	}

	switch key := pub.(type) {
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: invalid Ed25519 public key length %d", ErrUnsupportedKey, len(key))
		}
		if scheme != SchemeUnknown && scheme != Ed25519 {
			return fmt.Errorf("%w: %s cannot be used with Ed25519", ErrUnsupportedScheme, scheme)
		}
		if !ed25519.Verify(key, message, signature) {
			return ErrInvalidSignature
		}
		return nil

	case *ecdsa.PublicKey:
		if key == nil || key.Curve == nil {
			return fmt.Errorf("%w: nil ECDSA public key", ErrUnsupportedKey)
		}
		if scheme != SchemeUnknown && !scheme.isECDSA() {
			return fmt.Errorf("%w: %s cannot be used with ECDSA", ErrUnsupportedScheme, scheme)
		}
		digest, err := digestMessage(digestHash, message)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(key, digest, signature) {
			return ErrInvalidSignature
		}
		return nil

	case *rsa.PublicKey:
		if key == nil {
			return fmt.Errorf("%w: nil RSA public key", ErrUnsupportedKey)
		}
		if scheme != SchemeUnknown && !scheme.isRSA() {
			return fmt.Errorf("%w: %s cannot be used with RSA", ErrUnsupportedScheme, scheme)
		}
		digest, err := digestMessage(digestHash, message)
		if err != nil {
			return err
		}
		if scheme == SchemeUnknown || scheme.isPSS() {
			pssOptions := &rsa.PSSOptions{
				Hash:       digestHash,
				SaltLength: pssVerifySaltLength(options),
			}
			if err := rsa.VerifyPSS(key, digestHash, digest, signature, pssOptions); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(key, digestHash, digest, signature); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		return nil

	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedKey, pub)
	}
}

func normalizePublicKey(pub crypto.PublicKey) crypto.PublicKey {
	switch key := pub.(type) {
	case rsa.PublicKey:
		return &key
	case ecdsa.PublicKey:
		return &key
	case *x509.Certificate:
		if key != nil {
			return key.PublicKey
		}
	}
	return pub
}

// SignCertificateVerify signs the RFC 8446 CertificateVerify input for role
// and transcriptHash. It is primarily useful for tests and for peers that need
// to produce a CertificateVerify message; verification always reconstructs
// the context rather than trusting the signer.
func SignCertificateVerify(priv crypto.Signer, role Role, transcriptHash []byte, opts *SignOptions) ([]byte, error) {
	message, err := SignedMessage(role, transcriptHash)
	if err != nil {
		return nil, err
	}
	return SignSignedMessage(priv, message, opts)
}

// SignServerCertificateVerify signs a server CertificateVerify input.
func SignServerCertificateVerify(priv crypto.Signer, transcriptHash []byte, opts *SignOptions) ([]byte, error) {
	return SignCertificateVerify(priv, RoleServer, transcriptHash, opts)
}

// SignClientCertificateVerify signs a client CertificateVerify input.
func SignClientCertificateVerify(priv crypto.Signer, transcriptHash []byte, opts *SignOptions) ([]byte, error) {
	return SignCertificateVerify(priv, RoleClient, transcriptHash, opts)
}

// SignSignedMessage signs a complete CertificateVerify signed message after
// validating its context prefix.
func SignSignedMessage(priv crypto.Signer, message []byte, opts *SignOptions) ([]byte, error) {
	if err := ValidateSignedMessage(message); err != nil {
		return nil, err
	}
	return SignSignature(priv, 0, message, opts)
}

// SignSignature signs message using the TLS signature algorithm selected by
// opts. It is the signing counterpart of VerifySignature.
func SignSignature(priv crypto.Signer, hash crypto.Hash, message []byte, opts *SignOptions) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("%w: nil private key", ErrUnsupportedKey)
	}
	options := normalizeSignOptions(hash, opts)
	scheme, digestHash, err := resolveSchemeAndHash(priv.Public(), &VerifyOptions{
		Hash:   options.Hash,
		Scheme: options.Scheme,
	})
	if err != nil {
		return nil, err
	}
	random := options.Rand
	if random == nil {
		random = rand.Reader
	}

	switch key := priv.(type) {
	case ed25519.PrivateKey:
		if len(key) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 private key length %d", ErrUnsupportedKey, len(key))
		}
		if scheme != SchemeUnknown && scheme != Ed25519 {
			return nil, fmt.Errorf("%w: %s cannot be used with Ed25519", ErrUnsupportedScheme, scheme)
		}
		return ed25519.Sign(key, message), nil

	case *ecdsa.PrivateKey:
		if key == nil || key.Curve == nil {
			return nil, fmt.Errorf("%w: nil ECDSA private key", ErrUnsupportedKey)
		}
		if scheme != SchemeUnknown && !scheme.isECDSA() {
			return nil, fmt.Errorf("%w: %s cannot be used with ECDSA", ErrUnsupportedScheme, scheme)
		}
		digest, err := digestMessage(digestHash, message)
		if err != nil {
			return nil, err
		}
		return ecdsa.SignASN1(random, key, digest)

	case *rsa.PrivateKey:
		if key == nil {
			return nil, fmt.Errorf("%w: nil RSA private key", ErrUnsupportedKey)
		}
		if scheme != SchemeUnknown && !scheme.isRSA() {
			return nil, fmt.Errorf("%w: %s cannot be used with RSA", ErrUnsupportedScheme, scheme)
		}
		digest, err := digestMessage(digestHash, message)
		if err != nil {
			return nil, err
		}
		if scheme == SchemeUnknown || scheme.isPSS() {
			saltLength := options.PSSSaltLength
			if saltLength == 0 {
				saltLength = rsa.PSSSaltLengthEqualsHash
			}
			return rsa.SignPSS(random, key, digestHash, digest, &rsa.PSSOptions{
				SaltLength: saltLength,
				Hash:       digestHash,
			})
		}
		return rsa.SignPKCS1v15(random, key, digestHash, digest)

	default:
		// Fall back to crypto.Signer for opaque key implementations. For
		// RSA-PSS, pass PSS options so the signer does not silently choose
		// PKCS#1 v1.5.
		switch priv.Public().(type) {
		case ed25519.PublicKey:
			return priv.Sign(random, message, crypto.Hash(0))
		case *ecdsa.PublicKey:
			digest, err := digestMessage(digestHash, message)
			if err != nil {
				return nil, err
			}
			return priv.Sign(random, digest, digestHash)
		}
		if scheme == SchemeUnknown || scheme.isPSS() {
			digest, err := digestMessage(digestHash, message)
			if err != nil {
				return nil, err
			}
			saltLength := options.PSSSaltLength
			if saltLength == 0 {
				saltLength = rsa.PSSSaltLengthEqualsHash
			}
			return priv.Sign(random, digest, &rsa.PSSOptions{
				SaltLength: saltLength,
				Hash:       digestHash,
			})
		}
		digest, err := digestMessage(digestHash, message)
		if err != nil {
			return nil, err
		}
		return priv.Sign(random, digest, digestHash)
	}
}

func normalizeSignOptions(hash crypto.Hash, opts *SignOptions) SignOptions {
	if opts == nil {
		return SignOptions{Hash: hash}
	}
	options := *opts
	if options.Hash == 0 {
		options.Hash = hash
	}
	return options
}

func normalizeVerifyOptions(hash crypto.Hash, opts *VerifyOptions) VerifyOptions {
	if opts == nil {
		return VerifyOptions{Hash: hash}
	}
	options := *opts
	if options.Hash == 0 {
		options.Hash = hash
	}
	return options
}

func resolveSchemeAndHash(pub crypto.PublicKey, opts *VerifyOptions) (SignatureScheme, crypto.Hash, error) {
	scheme := opts.Scheme
	if scheme != SchemeUnknown {
		switch {
		case scheme.isPSS(), scheme.isPKCS1():
			if _, ok := pub.(*rsa.PublicKey); !ok {
				return 0, 0, fmt.Errorf("%w: %s requires an RSA key", ErrUnsupportedScheme, scheme)
			}
		case scheme.isECDSA():
			if _, ok := pub.(*ecdsa.PublicKey); !ok {
				return 0, 0, fmt.Errorf("%w: %s requires an ECDSA key", ErrUnsupportedScheme, scheme)
			}
		case scheme == Ed25519:
			if _, ok := pub.(ed25519.PublicKey); !ok {
				return 0, 0, fmt.Errorf("%w: ed25519 requires an Ed25519 key", ErrUnsupportedScheme)
			}
		default:
			return 0, 0, fmt.Errorf("%w: %s", ErrUnsupportedScheme, scheme)
		}
	}

	hash := opts.Hash
	if scheme == SchemeUnknown {
		if _, ok := pub.(ed25519.PublicKey); ok {
			if hash != 0 {
				return 0, 0, fmt.Errorf("%w: ed25519 does not use a separate digest", ErrUnsupportedHash)
			}
			return Ed25519, 0, nil
		}
	}
	if scheme != SchemeUnknown {
		schemeHash := scheme.Hash()
		if scheme == Ed25519 {
			if hash != 0 {
				return 0, 0, fmt.Errorf("%w: ed25519 does not use a separate digest", ErrUnsupportedHash)
			}
			return scheme, 0, nil
		}
		if schemeHash == 0 {
			return 0, 0, fmt.Errorf("%w: %s", ErrUnsupportedScheme, scheme)
		}
		if hash != 0 && hash != schemeHash {
			return 0, 0, fmt.Errorf("%w: hash %v does not match %s", ErrUnsupportedHash, hash, scheme)
		}
		hash = schemeHash
	}

	if hash == 0 {
		hash = defaultHashForPublicKey(pub)
	}
	if hash == 0 {
		return 0, 0, fmt.Errorf("%w for %T", ErrUnsupportedHash, pub)
	}
	if !hash.Available() {
		return 0, 0, fmt.Errorf("%w: %v is not linked", ErrUnsupportedHash, hash)
	}
	return scheme, hash, nil
}

func defaultHashForPublicKey(pub crypto.PublicKey) crypto.Hash {
	switch key := pub.(type) {
	case ed25519.PublicKey:
		return 0
	case *ecdsa.PublicKey:
		if key == nil || key.Curve == nil {
			return 0
		}
		switch key.Curve {
		case elliptic.P384():
			return crypto.SHA384
		case elliptic.P521():
			return crypto.SHA512
		default:
			return crypto.SHA256
		}
	default:
		return crypto.SHA256
	}
}

func digestMessage(hash crypto.Hash, message []byte) ([]byte, error) {
	if hash == 0 {
		return nil, fmt.Errorf("%w: zero hash", ErrUnsupportedHash)
	}
	if !hash.Available() {
		return nil, fmt.Errorf("%w: %v is not linked", ErrUnsupportedHash, hash)
	}
	h := hash.New()
	_, _ = h.Write(message)
	return h.Sum(nil), nil
}

func pssVerifySaltLength(opts VerifyOptions) int {
	if opts.PSSSaltLength != 0 {
		return opts.PSSSaltLength
	}
	if opts.RequirePSSSaltLengthEqualsHash {
		return rsa.PSSSaltLengthEqualsHash
	}
	return rsa.PSSSaltLengthAuto
}
