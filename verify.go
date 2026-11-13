package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

// Verify is a convenience dispatcher around the typed verification helpers.
// New code should normally call VerifyCertificateVerify or
// VerifySignedMessage directly.
//
// The following argument shapes are accepted, in any key/role order:
//
//	Verify(pub, role, transcriptHash, signature[, opts])
//	Verify(pub, context, transcriptHash, signature[, opts])
//	Verify(pub, hash, signedMessage, signature[, opts])
//	Verify(signedMessage, signature, pub[, opts])
//
// pub may be *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey,
// crypto.Signer, or *x509.Certificate. A string role is "server" or
// "client" (or its RFC 8446 context label). A full context, or a context
// missing spaces, is validated as a context and is never repaired.
func Verify(args ...any) error {
	if len(args) == 0 {
		return errors.New("netsec: Verify requires arguments")
	}

	var (
		role    Role
		context []byte
		message []byte
		blobs   [][]byte
		pub     crypto.PublicKey
		opts    *VerifyOptions
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue

		case *VerifyOptions:
			opts = value
		case VerifyOptions:
			copy := value
			opts = &copy
		case crypto.Hash:
			opts = withHash(opts, value)
		case SignatureScheme:
			opts = withScheme(opts, value)
		case *rsa.PSSOptions:
			opts = withHash(opts, value.Hash)
			if value.SaltLength != 0 {
				opts = withPSSSaltLength(opts, value.SaltLength)
			}

		case string:
			if isContextString(value) || isShortenedContext(value) {
				context = []byte(value)
				continue
			}
			if parsed, err := ParseRole(value); err == nil {
				role = parsed
				continue
			}
			if scheme, ok := parseSchemeName(value); ok {
				opts = withScheme(opts, scheme)
				continue
			}
			return fmt.Errorf("%w: %q", ErrInvalidRole, value)

		case []byte:
			switch classifyBytes(value) {
			case bytesAreMessage:
				message = value
			case bytesAreContext:
				context = value
			default:
				blobs = append(blobs, value)
			}

		case *rsa.PublicKey:
			pub = value
		case rsa.PublicKey:
			copy := value
			pub = &copy
		case *ecdsa.PublicKey:
			pub = value
		case ecdsa.PublicKey:
			copy := value
			pub = &copy
		case ed25519.PublicKey:
			pub = value
		case crypto.Signer:
			pub = value.Public()
		case *x509.Certificate:
			pub = value.PublicKey

		default:
			return fmt.Errorf("%w: %T", ErrUnsupportedKey, arg)
		}
	}

	if pub == nil {
		return fmt.Errorf("%w: no public key", ErrUnsupportedKey)
	}

	switch {
	case message != nil:
		signature, err := signatureBlob(blobs)
		if err != nil {
			return err
		}
		return VerifySignedMessage(pub, message, signature, opts)

	case context != nil:
		transcript, signature, err := transcriptAndSignature(blobs)
		if err != nil {
			return err
		}
		return VerifyContext(pub, context, transcript, signature, opts)

	case role != "":
		transcript, signature, err := transcriptAndSignature(blobs)
		if err != nil {
			return err
		}
		return VerifyCertificateVerify(pub, role, transcript, signature, opts)

	default:
		// Lower-level form: Verify(pub, hash, signedMessage, signature).
		transcript, signature, err := transcriptAndSignature(blobs)
		if err != nil {
			return err
		}
		var hash crypto.Hash
		if opts != nil {
			hash = opts.Hash
		}
		return VerifySignature(pub, hash, transcript, signature, opts)
	}
}

// VerifyBool is the bool-returning form of Verify.
func VerifyBool(args ...any) bool {
	return Verify(args...) == nil
}

// Check is an alias for Verify.
func Check(args ...any) error {
	return Verify(args...)
}

// VerifyCertificateVerifyContext verifies a signature using an explicit
// context. Unlike VerifyCertificateVerify, the caller supplies the context,
// so invalid or shortened contexts can be tested directly.
func VerifyCertificateVerifyContext(pub crypto.PublicKey, context, transcriptHash, signature []byte, opts *VerifyOptions) error {
	return VerifyContext(pub, context, transcriptHash, signature, opts)
}

// VerifyWithContext is an alias for VerifyCertificateVerifyContext.
func VerifyWithContext(pub crypto.PublicKey, context, transcriptHash, signature []byte, opts *VerifyOptions) error {
	return VerifyContext(pub, context, transcriptHash, signature, opts)
}

// VerifyServerCertificateVerify verifies a server CertificateVerify
// signature.
func VerifyServerCertificateVerify(pub crypto.PublicKey, transcriptHash, signature []byte, opts *VerifyOptions) error {
	return VerifyCertificateVerify(pub, RoleServer, transcriptHash, signature, opts)
}

// VerifyClientCertificateVerify verifies a client CertificateVerify
// signature.
func VerifyClientCertificateVerify(pub crypto.PublicKey, transcriptHash, signature []byte, opts *VerifyOptions) error {
	return VerifyCertificateVerify(pub, RoleClient, transcriptHash, signature, opts)
}

// Sign is a convenience dispatcher for signing. Accepted forms include:
//
//	Sign(priv, role, transcriptHash[, opts])
//	Sign(priv, signedMessage[, opts])
//	Sign(priv, hash, message[, opts])
func Sign(args ...any) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("netsec: Sign requires arguments")
	}

	var (
		priv    crypto.Signer
		role    Role
		message []byte
		blobs   [][]byte
		hash    crypto.Hash
		opts    *SignOptions
	)

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case *SignOptions:
			opts = value
		case SignOptions:
			copy := value
			opts = &copy
		case crypto.Hash:
			hash = value
			if opts == nil {
				opts = &SignOptions{}
			}
			opts.Hash = value
		case SignatureScheme:
			if opts == nil {
				opts = &SignOptions{}
			}
			opts.Scheme = value
		case string:
			parsed, err := ParseRole(value)
			if err != nil {
				return nil, err
			}
			role = parsed
		case crypto.Signer:
			priv = value
		case []byte:
			if isMessageBytes(value) {
				message = value
			} else {
				blobs = append(blobs, value)
			}
		default:
			return nil, fmt.Errorf("%w: %T", ErrUnsupportedKey, arg)
		}
	}

	if priv == nil {
		return nil, fmt.Errorf("%w: no private key", ErrUnsupportedKey)
	}
	if message != nil {
		return SignSignedMessage(priv, message, opts)
	}
	if role != "" {
		transcript, err := singleBlob(blobs)
		if err != nil {
			return nil, err
		}
		return SignCertificateVerify(priv, role, transcript, opts)
	}
	transcript, err := singleBlob(blobs)
	if err != nil {
		return nil, err
	}
	return SignSignature(priv, hash, transcript, opts)
}

type byteKind int

const (
	bytesAreUnknown byteKind = iota
	bytesAreContext
	bytesAreMessage
)

func classifyBytes(value []byte) byteKind {
	spaces := leadingSpaces(value)
	rest := value[spaces:]

	if spaces >= ContextPrefixLength-1 {
		switch {
		case bytes.HasPrefix(rest, []byte(ServerContextLabel+"\x00")),
			bytes.HasPrefix(rest, []byte(ClientContextLabel+"\x00")):
			return bytesAreMessage
		case bytes.Equal(rest, []byte(ServerContextLabel)),
			bytes.Equal(rest, []byte(ClientContextLabel)):
			return bytesAreContext
		}
	}
	if spaces > 0 && len(value) >= ContextPrefixLength+len(ServerContextLabel)-1 {
		return bytesAreContext
	}
	if isContextString(string(value)) || isShortenedContext(string(value)) {
		return bytesAreContext
	}
	return bytesAreUnknown
}

func isContextString(value string) bool {
	return value == ServerContext || value == ClientContext
}

func isShortenedContext(value string) bool {
	spaces := 0
	for spaces < len(value) && value[spaces] == ' ' {
		spaces++
	}
	if spaces == 0 {
		return value == ServerContextLabel || value == ClientContextLabel
	}
	rest := value[spaces:]
	return rest == ServerContextLabel || rest == ClientContextLabel
}

func isMessageBytes(value []byte) bool {
	return classifyBytes(value) == bytesAreMessage
}

func transcriptAndSignature(blobs [][]byte) ([]byte, []byte, error) {
	if len(blobs) != 2 {
		return nil, nil, fmt.Errorf("netsec: need transcript hash and signature, got %d byte arguments", len(blobs))
	}
	return blobs[0], blobs[1], nil
}

func signatureBlob(blobs [][]byte) ([]byte, error) {
	if len(blobs) != 1 {
		return nil, fmt.Errorf("netsec: need one signature, got %d byte arguments", len(blobs))
	}
	return blobs[0], nil
}

func singleBlob(blobs [][]byte) ([]byte, error) {
	if len(blobs) != 1 {
		return nil, fmt.Errorf("netsec: need one transcript/message argument, got %d", len(blobs))
	}
	return blobs[0], nil
}

func withHash(opts *VerifyOptions, hash crypto.Hash) *VerifyOptions {
	if opts == nil {
		opts = &VerifyOptions{}
	}
	opts.Hash = hash
	return opts
}

func withScheme(opts *VerifyOptions, scheme SignatureScheme) *VerifyOptions {
	if opts == nil {
		opts = &VerifyOptions{}
	}
	opts.Scheme = scheme
	return opts
}

func withPSSSaltLength(opts *VerifyOptions, length int) *VerifyOptions {
	if opts == nil {
		opts = &VerifyOptions{}
	}
	opts.PSSSaltLength = length
	return opts
}

func parseSchemeName(name string) (SignatureScheme, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "rsa_pkcs1_sha1":
		return PKCS1WithSHA1, true
	case "rsa_pkcs1_sha256":
		return PKCS1WithSHA256, true
	case "rsa_pkcs1_sha384":
		return PKCS1WithSHA384, true
	case "rsa_pkcs1_sha512":
		return PKCS1WithSHA512, true
	case "ecdsa_secp256r1_sha256":
		return ECDSAWithP256AndSHA256, true
	case "ecdsa_secp384r1_sha384":
		return ECDSAWithP384AndSHA384, true
	case "ecdsa_secp521r1_sha512":
		return ECDSAWithP521AndSHA512, true
	case "rsa_pss_rsae_sha256", "rsa_pss_pss_sha256":
		return PSSWithSHA256, true
	case "rsa_pss_rsae_sha384", "rsa_pss_pss_sha384":
		return PSSWithSHA384, true
	case "rsa_pss_rsae_sha512", "rsa_pss_pss_sha512":
		return PSSWithSHA512, true
	case "ed25519":
		return Ed25519, true
	default:
		return SchemeUnknown, false
	}
}
