package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"hash"
	"math/big"
	"time"
)

// CertTypeUser marks an SSH user certificate (cert_type == 1).
const CertTypeUser uint32 = 1

// Certificate is the parsed, not yet authorized, view of an SSH user
// certificate (PROTOCOL.certkeys, version 01).
type Certificate struct {
	// Raw is the exact wire blob presented by the client.
	Raw []byte

	Serial          uint64
	Type            uint32
	KeyID           string
	ValidPrincipals []string

	// ValidAfter/ValidBefore are zero when the certificate has no lower
	// or upper bound respectively. The validity window is half-open:
	// ValidAfter <= now < ValidBefore.
	ValidAfter  time.Time
	ValidBefore time.Time

	// CriticalOptions maps option name to value. The only recognized
	// options are force-command and source-address.
	CriticalOptions map[string]string
	// Extensions are informational for user certificates and never
	// influence authorization.
	Extensions map[string]string

	// Key is the public key embedded in the certificate; it is what the
	// client must prove possession of during the SSH handshake.
	Key PublicKey

	// SignatureType is the SSH signature algorithm name.
	SignatureType string
	signature     []byte

	// signedBlob is Raw minus the trailing signature field, i.e. the
	// exact bytes that the CA signed.
	signedBlob []byte
}

// Critical option names defined by PROTOCOL.certkeys.
const (
	OptionForceCommand  = "force-command"
	OptionSourceAddress = "source-address"
)

// ParseCertificate parses an SSH certificate wire blob. It performs only
// structural decoding; trust and policy checks happen in Authorizer.
func ParseCertificate(raw []byte) (*Certificate, error) {
	if len(raw) == 0 {
		return nil, ErrMalformedCertificate
	}
	r := &wireReader{full: raw}

	certType := r.string()
	if r.err != nil {
		return nil, r.err
	}
	if baseKeyType(certType) == "" {
		return nil, ErrUnsupportedCertificate
	}

	nonce := r.stringBytes()
	if r.err != nil || len(nonce) == 0 {
		return nil, ErrMalformedCertificate
	}

	key, err := parseKeyBlob(r)
	if err != nil {
		return nil, err
	}

	c := &Certificate{
		Raw:             append([]byte(nil), raw...),
		Serial:          r.uint32(),
		Type:            uint32(r.uint32()),
		KeyID:           r.string(),
		ValidPrincipals: r.nameList(),
	}

	after := r.uint32()
	before := r.uint32()
	if after != 0 {
		c.ValidAfter = time.Unix(int64(after), 0).UTC()
	}
	if before != 0 {
		c.ValidBefore = time.Unix(int64(before), 0).UTC()
	}

	c.CriticalOptions, err = readCertOptions(r)
	if err != nil {
		return nil, err
	}
	c.Extensions, err = readCertOptions(r)
	if err != nil {
		return nil, err
	}

	reserved := r.stringBytes()
	if r.err != nil || len(reserved) != 0 {
		return nil, ErrMalformedCertificate
	}

	sigBlob := r.stringBytes()
	if r.err != nil || r.remaining() != 0 {
		return nil, ErrMalformedCertificate
	}

	sigReader := &wireReader{full: sigBlob}
	c.SignatureType = sigReader.string()
	c.signature = sigReader.stringBytes()
	if sigReader.err != nil || sigReader.remaining() != 0 || len(c.signature) == 0 {
		return nil, ErrMalformedCertificate
	}

	if baseKeyType(certType) != key.Type() {
		return nil, ErrMalformedCertificate
	}

	sigLen := 4 + len(sigBlob)
	c.signedBlob = append([]byte(nil), raw[:len(raw)-sigLen]...)
	c.Key = key
	return c, nil
}

func readCertOptions(r *wireReader) (map[string]string, error) {
	raw := r.stringBytes()
	if r.err != nil {
		return nil, r.err
	}
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	sr := &wireReader{full: raw}
	count := sr.uint32()
	out := make(map[string]string, count)
	for i := uint64(0); i < count; i++ {
		name := sr.string()
		if sr.err != nil {
			return nil, ErrMalformedCertificate
		}
		if _, exists := out[name]; exists {
			return nil, ErrMalformedCertificate
		}
		value := sr.string()
		if sr.err != nil {
			return nil, ErrMalformedCertificate
		}
		out[name] = value
	}
	if sr.remaining() != 0 {
		return nil, ErrMalformedCertificate
	}
	return out, nil
}

// verifyWith checks the CA signature over the certificate blob.
// The signed data is the certificate bytes without the trailing signature
// field, as mandated by PROTOCOL.certkeys section 2.
func (c *Certificate) verifyWith(ca PublicKey) error {
	signed := make([]byte, len(c.signedBlob))
	copy(signed, c.signedBlob)

	key, ok := ca.(*publicKey)
	if !ok {
		return ErrUntrustedCA
	}
	if key.ed25519 == nil && key.rsa == nil && key.ecdsa == nil {
		return ErrUntrustedCA
	}

	switch k := key.keyType; k {
	case "ssh-ed25519":
		if key.ed25519 == nil {
			return ErrBadSignature
		}
		if c.SignatureType != "ssh-ed25519" {
			return ErrBadSignature
		}
		if !ed25519.Verify(key.ed25519, signed, c.signature) {
			return ErrBadSignature
		}

	case "ssh-rsa":
		if key.rsa == nil {
			return ErrBadSignature
		}
		h, hashID, err := rsaHash(c.SignatureType)
		if err != nil {
			return err
		}
		h.Write(signed)
		if err := rsa.VerifyPKCS1v15(key.rsa, hashID, h.Sum(nil), c.signature); err != nil {
			return ErrBadSignature
		}

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		if key.ecdsa == nil {
			return ErrBadSignature
		}
		if c.SignatureType != k {
			return ErrBadSignature
		}
		if name, curveOK := ecdsaCurveName(key.ecdsa.Curve); !curveOK || "ecdsa-sha2-"+name != k {
			return ErrBadSignature
		}
		h, err := ecdsaHash(k)
		if err != nil {
			return err
		}
		h.Write(signed)
		sig, err := parseECDSASignature(c.signature)
		if err != nil {
			return ErrBadSignature
		}
		if !ecdsa.Verify(key.ecdsa, h.Sum(nil), sig.R, sig.S) {
			return ErrBadSignature
		}

	default:
		return ErrUntrustedCA
	}
	return nil
}

func rsaHash(alg string) (hash.Hash, crypto.Hash, error) {
	switch alg {
	case "rsa-sha2-256":
		return sha256.New(), crypto.SHA256, nil
	case "rsa-sha2-512":
		return sha512.New(), crypto.SHA512, nil
	default:
		// The legacy ssh-rsa / SHA-1 algorithm is intentionally rejected.
		return nil, 0, ErrBadSignature
	}
}

func ecdsaHash(alg string) (hash.Hash, error) {
	switch alg {
	case "ecdsa-sha2-nistp256":
		return sha256.New(), nil
	case "ecdsa-sha2-nistp384":
		return sha512.New384(), nil
	case "ecdsa-sha2-nistp521":
		return sha512.New(), nil
	default:
		return nil, ErrBadSignature
	}
}

func parseECDSASignature(blob []byte) (struct{ R, S *big.Int }, error) {
	var out struct{ R, S *big.Int }
	r := &wireReader{full: blob}
	out.R = new(big.Int).SetBytes(r.stringBytes())
	out.S = new(big.Int).SetBytes(r.stringBytes())
	if r.err != nil || r.remaining() != 0 || out.R.Sign() <= 0 || out.S.Sign() <= 0 {
		return out, ErrBadSignature
	}
	return out, nil
}

var (
	ErrMalformedCertificate   = errors.New("netsec: malformed ssh certificate")
	ErrUnsupportedCertificate = errors.New("netsec: unsupported ssh certificate type")
	ErrBadSignature           = errors.New("netsec: certificate signature is invalid")
)
