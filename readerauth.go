package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// OIDReaderAuthEKU is the ISO/IEC 18013-5 extended key usage that a reader
// (mdoc reader) certificate MUST carry: id-iso-mdoc-readerauth
// {iso(1) standard(0) 18013 5 1 6}.
var OIDReaderAuthEKU = asn1.ObjectIdentifier{1, 0, 18013, 5, 1, 6}

var (
	// ErrReaderUsageMissing is returned when the reader certificate does
	// not carry the reader extended key usage.
	ErrReaderUsageMissing = errors.New("netsec: reader certificate lacks the reader extended key usage")
	// ErrTranscriptIncomplete is returned when the session transcript does
	// not contain the handover, the reader key and the device key.
	ErrTranscriptIncomplete = errors.New("netsec: session transcript must contain device engagement, reader key and handover")
	// ErrSignatureMismatch is returned when the reader authentication
	// signature does not cover the supplied session transcript.
	ErrSignatureMismatch = errors.New("netsec: reader authentication signature does not match the session transcript")
	// ErrMalformed is returned when a COSE_Sign1 structure cannot be parsed.
	ErrMalformed = errors.New("netsec: malformed reader authentication")
)

// SessionTranscript binds a reader authentication to one specific session.
// All fields are already CBOR-encoded values as exchanged during engagement;
// the transcript is:
//
//	SessionTranscript = [ DeviceEngagementBytes, EReaderKeyBytes, Handover ]
type SessionTranscript struct {
	DeviceEngagement []byte // encoded DeviceEngagement, carries the device key
	ReaderKey        []byte // encoded EReaderKey (COSE_Key)
	Handover         []byte // encoded Handover structure
}

func (t SessionTranscript) validate() error {
	if len(t.DeviceEngagement) == 0 || len(t.ReaderKey) == 0 || len(t.Handover) == 0 {
		return ErrTranscriptIncomplete
	}
	return nil
}

// encode produces the CBOR encoding of the session transcript.
func (t SessionTranscript) encode() ([]byte, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	w := &cborWriter{}
	w.array(3)
	w.buf = append(w.buf, taggedBytes(t.DeviceEngagement)...)
	w.buf = append(w.buf, taggedBytes(t.ReaderKey)...)
	w.bytes(t.Handover)
	return w.buf, nil
}

// ReaderAuthenticationBytes builds the to-be-authenticated payload:
//
//	ReaderAuthentication = [ "ReaderAuthentication", SessionTranscript, ItemsRequestBytes ]
func ReaderAuthenticationBytes(t SessionTranscript, itemsRequest []byte) ([]byte, error) {
	transcript, err := t.encode()
	if err != nil {
		return nil, err
	}
	w := &cborWriter{}
	w.array(3)
	w.text("ReaderAuthentication")
	w.buf = append(w.buf, transcript...)
	w.buf = append(w.buf, taggedBytes(itemsRequest)...)
	return w.buf, nil
}

// cose algorithm identifiers.
const (
	coseAlgES256 = -7
	coseAlgES384 = -35
	coseAlgES512 = -36
	coseAlgRS256 = -257
)

func keyAlg(pub crypto.PublicKey) (alg int64, hash crypto.Hash, err error) {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return coseAlgES256, crypto.SHA256, nil
		case elliptic.P384():
			return coseAlgES384, crypto.SHA384, nil
		case elliptic.P521():
			return coseAlgES512, crypto.SHA512, nil
		}
	case *rsa.PublicKey:
		return coseAlgRS256, crypto.SHA256, nil
	}
	return 0, 0, fmt.Errorf("netsec: unsupported reader key type %T", pub)
}

// protectedHeader returns the encoded protected header map {1: alg}.
func protectedHeader(alg int64) []byte {
	w := &cborWriter{}
	w.mapHead(1)
	w.int64Val(1) // label: alg
	w.int64Val(alg)
	return w.buf
}

// sigStructure builds the COSE Sig_structure that is actually signed:
//
//	[ "Signature1", protected, external_aad, payload ]
func sigStructure(protected, payload []byte) []byte {
	w := &cborWriter{}
	w.array(4)
	w.text("Signature1")
	w.bytes(protected)
	w.bytes(nil) // external_aad
	w.bytes(payload)
	return w.buf
}

func digest(hash crypto.Hash, data []byte) []byte {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil)
}

func ecdsaRawSize(pub *ecdsa.PublicKey) int {
	return (pub.Curve.Params().BitSize + 7) / 8
}

// SignReaderAuth produces a COSE_Sign1 reader authentication over the given
// session transcript and items request. It is used by readers; wallets call
// VerifyReaderAuth.
func SignReaderAuth(key crypto.Signer, t SessionTranscript, itemsRequest []byte, random io.Reader) ([]byte, error) {
	if random == nil {
		random = rand.Reader
	}
	payload, err := ReaderAuthenticationBytes(t, itemsRequest)
	if err != nil {
		return nil, err
	}
	alg, hash, err := keyAlg(key.Public())
	if err != nil {
		return nil, err
	}
	protected := protectedHeader(alg)
	toSign := sigStructure(protected, payload)
	d := digest(hash, toSign)

	var sig []byte
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(random, k, d)
		if err != nil {
			return nil, err
		}
		n := ecdsaRawSize(&k.PublicKey)
		sig = make([]byte, 2*n)
		r.FillBytes(sig[:n])
		s.FillBytes(sig[n:])
	case *rsa.PrivateKey:
		sig, err = rsa.SignPKCS1v15(random, k, hash, d)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("netsec: unsupported reader key type %T", key)
	}

	w := &cborWriter{}
	w.array(4)
	w.bytes(protected)
	w.mapHead(0) // unprotected header
	w.bytes(payload)
	w.bytes(sig)
	return w.buf, nil
}

// VerifyOptions optionally pins certificate chain validation for
// VerifyReaderAuth. When Roots is nil only the extended key usage and the
// signature are checked.
type VerifyOptions struct {
	Roots *x509.CertPool
}

// HasReaderUsage reports whether cert carries the reader extended key usage.
func HasReaderUsage(cert *x509.Certificate) bool {
	for _, oid := range cert.UnknownExtKeyUsage {
		if oid.Equal(OIDReaderAuthEKU) {
			return true
		}
	}
	return false
}

// VerifyReaderAuth verifies a reader authentication on behalf of a wallet
// (mdoc). It fails unless the reader certificate carries the reader extended
// key usage AND the signature covers a session transcript that contains the
// handover, the reader key and the device key exactly as supplied.
func VerifyReaderAuth(cert *x509.Certificate, readerAuth []byte, t SessionTranscript, itemsRequest []byte, opts *VerifyOptions) error {
	if cert == nil {
		return fmt.Errorf("netsec: nil reader certificate")
	}
	if !HasReaderUsage(cert) {
		return ErrReaderUsageMissing
	}
	if opts != nil && opts.Roots != nil {
		if _, err := cert.Verify(x509.VerifyOptions{
			Roots:     opts.Roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}); err != nil {
			return fmt.Errorf("netsec: reader certificate chain: %w", err)
		}
	}

	payload, err := ReaderAuthenticationBytes(t, itemsRequest)
	if err != nil {
		return err
	}

	protected, embeddedPayload, sig, err := parseCOSESign1(readerAuth)
	if err != nil {
		return err
	}
	if len(embeddedPayload) != 0 && !equalBytes(embeddedPayload, payload) {
		return ErrSignatureMismatch
	}

	alg, hash, err := keyAlg(cert.PublicKey)
	if err != nil {
		return err
	}
	if want := protectedHeader(alg); !equalBytes(protected, want) {
		return fmt.Errorf("netsec: unexpected protected header in reader authentication")
	}

	toVerify := sigStructure(protected, payload)
	d := digest(hash, toVerify)

	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		n := ecdsaRawSize(pub)
		if len(sig) != 2*n {
			return ErrSignatureMismatch
		}
		r := new(big.Int).SetBytes(sig[:n])
		s := new(big.Int).SetBytes(sig[n:])
		if !ecdsa.Verify(pub, d, r, s) {
			return ErrSignatureMismatch
		}
		return nil
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(pub, hash, d, sig); err != nil {
			return ErrSignatureMismatch
		}
		return nil
	default:
		return fmt.Errorf("netsec: unsupported reader key type %T", cert.PublicKey)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// parseCOSESign1 decodes [protected, unprotected, payload, signature].
func parseCOSESign1(data []byte) (protected, payload, sig []byte, err error) {
	r := &cborReader{data: data}
	n, err := r.array()
	if err != nil || n != 4 {
		return nil, nil, nil, ErrMalformed
	}
	if protected, err = r.bytes(); err != nil {
		return nil, nil, nil, ErrMalformed
	}
	if err = r.skip(); err != nil { // unprotected header
		return nil, nil, nil, ErrMalformed
	}
	if payload, err = r.bytes(); err != nil {
		return nil, nil, nil, ErrMalformed
	}
	if sig, err = r.bytes(); err != nil {
		return nil, nil, nil, ErrMalformed
	}
	if r.off != len(r.data) {
		return nil, nil, nil, ErrMalformed
	}
	return protected, payload, sig, nil
}
