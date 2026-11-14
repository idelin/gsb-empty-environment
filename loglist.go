package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
)

// Log is a single trusted Certificate Transparency log.
type Log struct {
	Name string
	// Key is the log's public key: *ecdsa.PublicKey or *rsa.PublicKey.
	Key crypto.PublicKey
	// ID is the SHA-256 of the log's DER SubjectPublicKeyInfo (RFC 6962 s3.2).
	ID [32]byte
}

// NewLog builds a trusted Log from its public key, deriving the log ID.
func NewLog(name string, key crypto.PublicKey) (*Log, error) {
	switch key.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey:
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedLogKey, key)
	}
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: marshal log key: %w", err)
	}
	return &Log{Name: name, Key: key, ID: sha256.Sum256(der)}, nil
}

// LogList is one generation of the trusted CT log set.
type LogList struct {
	// Generation identifies this revision of the trust list; policies and
	// lists take effect together per generation.
	Generation uint64
	Logs       []*Log
}

// ByID returns the trusted log with the given ID, or nil.
func (ll *LogList) ByID(id [32]byte) *Log {
	for _, l := range ll.Logs {
		if l != nil && l.ID == id {
			return l
		}
	}
	return nil
}

// ErrUnsupportedLogKey is returned for log keys that are neither ECDSA nor RSA.
var ErrUnsupportedLogKey = errors.New("netsec: unsupported log key type")

// ErrUnsupportedHashAlgorithm is returned for weak or unknown SCT hash algorithms.
var ErrUnsupportedHashAlgorithm = errors.New("netsec: unsupported SCT hash algorithm")

// ErrSignatureAlgorithmMismatch is returned when the SCT signature algorithm
// does not match the log key type.
var ErrSignatureAlgorithmMismatch = errors.New("netsec: SCT signature algorithm does not match log key")

// ErrSignatureMismatch is returned when the log signature does not verify
// against the recomputed entry for the certificate under validation.
var ErrSignatureMismatch = errors.New("netsec: SCT signature does not cover this certificate")

// hashFor maps an SCT hash algorithm to a crypto.Hash, rejecting weak ones.
func hashFor(h HashAlgorithm) (crypto.Hash, error) {
	switch h {
	case HashSHA256:
		return crypto.SHA256, nil
	case HashSHA384:
		return crypto.SHA384, nil
	case HashSHA512:
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("%w: %d", ErrUnsupportedHashAlgorithm, h)
	}
}

// verify checks the SCT's digitally-signed struct over input with the log key.
func (l *Log) verify(sct *SCT, input []byte) error {
	hash, err := hashFor(sct.HashAlgorithm)
	if err != nil {
		return err
	}
	h := hash.New()
	h.Write(input)
	digest := h.Sum(nil)
	switch key := l.Key.(type) {
	case *ecdsa.PublicKey:
		if sct.SignatureAlgorithm != SigECDSA {
			return fmt.Errorf("%w: log %q uses ECDSA", ErrSignatureAlgorithmMismatch, l.Name)
		}
		if !ecdsa.VerifyASN1(key, digest, sct.Signature) {
			return fmt.Errorf("%w (log %q)", ErrSignatureMismatch, l.Name)
		}
	case *rsa.PublicKey:
		if sct.SignatureAlgorithm != SigRSA {
			return fmt.Errorf("%w: log %q uses RSA", ErrSignatureAlgorithmMismatch, l.Name)
		}
		if rsa.VerifyPKCS1v15(key, hash, digest, sct.Signature) != nil {
			return fmt.Errorf("%w (log %q)", ErrSignatureMismatch, l.Name)
		}
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedLogKey, l.Key)
	}
	return nil
}
