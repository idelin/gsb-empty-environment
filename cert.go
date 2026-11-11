package knownhosts

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"
	"time"
)

const hostCertificateType = 1

type cursor struct {
	data []byte
	off  int
}

func (c *cursor) readUint32() (uint32, error) {
	if c.off+4 > len(c.data) {
		return 0, errShortBuffer
	}
	value := binary.BigEndian.Uint32(c.data[c.off:])
	c.off += 4
	return value, nil
}

func (c *cursor) readUint64() (uint64, error) {
	if c.off+8 > len(c.data) {
		return 0, errShortBuffer
	}
	value := binary.BigEndian.Uint64(c.data[c.off:])
	c.off += 8
	return value, nil
}

func (c *cursor) readString() ([]byte, error) {
	length, err := c.readUint32()
	if err != nil {
		return nil, err
	}
	if int(length) > len(c.data)-c.off {
		return nil, errShortBuffer
	}
	value := c.data[c.off : c.off+int(length)]
	c.off += int(length)
	return value, nil
}

type sshCertificate struct {
	certType     string
	principals   []string
	signatureKey []byte
	signed       []byte
	sigFormat    string
	sigBlob      []byte
	validAfter   uint64
	validBefore  uint64
}

// parseCertificate walks an SSH certificate wire blob. Only the fields needed
// for trust decisions are interpreted; other fields are skipped by length.
func parseCertificate(blob []byte) (*sshCertificate, error) {
	c := &cursor{data: blob}
	cert := &sshCertificate{}

	name, err := c.readString()
	if err != nil {
		return nil, err
	}
	cert.certType = string(name)

	if _, err := c.readString(); err != nil { // nonce
		return nil, err
	}

	switch {
	case strings.HasPrefix(cert.certType, "ssh-rsa-cert-"):
		if _, err := c.readString(); err != nil { // e
			return nil, err
		}
		if _, err := c.readString(); err != nil { // n
			return nil, err
		}
	case strings.HasPrefix(cert.certType, "ecdsa-sha2-"):
		if _, err := c.readString(); err != nil { // curve
			return nil, err
		}
		if _, err := c.readString(); err != nil { // Q
			return nil, err
		}
	case strings.HasPrefix(cert.certType, "ssh-ed25519-cert-"):
		if _, err := c.readString(); err != nil { // public key
			return nil, err
		}
	case strings.HasPrefix(cert.certType, "ssh-dss-cert-"):
		for range 4 {
			if _, err := c.readString(); err != nil {
				return nil, err
			}
		}
	case strings.Contains(cert.certType, "-sk-"):
		if _, err := c.readString(); err != nil { // application
			return nil, err
		}
		return nil, errUnsupported
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupported, cert.certType)
	}

	if _, err := c.readUint64(); err != nil { // serial
		return nil, err
	}
	certKind, err := c.readUint32()
	if err != nil {
		return nil, err
	}
	if certKind != hostCertificateType {
		return nil, fmt.Errorf("not a host certificate (type %d)", certKind)
	}
	if _, err := c.readString(); err != nil { // key id
		return nil, err
	}

	principalBlob, err := c.readString()
	if err != nil {
		return nil, err
	}
	pc := &cursor{data: principalBlob}
	count, err := pc.readUint32()
	if err != nil {
		return nil, err
	}
	for range count {
		principal, err := pc.readString()
		if err != nil {
			return nil, err
		}
		cert.principals = append(cert.principals, string(principal))
	}

	if cert.validAfter, err = c.readUint64(); err != nil {
		return nil, err
	}
	if cert.validBefore, err = c.readUint64(); err != nil {
		return nil, err
	}
	if _, err := c.readString(); err != nil { // critical options
		return nil, err
	}
	if _, err := c.readString(); err != nil { // extensions
		return nil, err
	}
	if _, err := c.readString(); err != nil { // reserved
		return nil, err
	}

	if cert.signatureKey, err = c.readString(); err != nil {
		return nil, err
	}

	// Everything up to the final signature field is what the CA signs.
	signedEnd := c.off
	sigField, err := c.readString()
	if err != nil {
		return nil, err
	}
	cert.signed = blob[:signedEnd]

	sc := &cursor{data: sigField}
	format, err := sc.readString()
	if err != nil {
		return nil, err
	}
	if cert.sigBlob, err = sc.readString(); err != nil {
		return nil, err
	}
	cert.sigFormat = string(format)

	return cert, nil
}

// verifyCertificate checks a presented certificate against a single
// cert-authority key blob: CA identity, principal list, validity window and
// the CA signature.
func verifyCertificate(certBlob, caBlob []byte, host string) error {
	cert, err := parseCertificate(certBlob)
	if err != nil {
		return err
	}
	if !bytes.Equal(cert.signatureKey, caBlob) {
		return errors.New("certificate was not signed by the recorded certificate authority")
	}
	if len(cert.principals) > 0 {
		matched := false
		for _, principal := range cert.principals {
			if globMatch(principal, host) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: %s", errBadPrincipal, host)
		}
	}
	now := uint64(time.Now().Unix())
	if now < cert.validAfter || now > cert.validBefore {
		return errors.New("certificate is outside its validity interval")
	}
	return verifyCertSignature(cert)
}

func verifyCertSignature(cert *sshCertificate) error {
	caType, err := keyTypeOf(cert.signatureKey)
	if err != nil {
		return err
	}

	switch caType {
	case "ssh-ed25519":
		c := &cursor{data: cert.signatureKey}
		if _, err := c.readString(); err != nil {
			return err
		}
		pub, err := c.readString()
		if err != nil {
			return err
		}
		if len(pub) != ed25519.PublicKeySize ||
			!ed25519.Verify(ed25519.PublicKey(pub), cert.signed, cert.sigBlob) {
			return errBadSignature
		}
		return nil

	case "ssh-rsa":
		pub, err := parseRSAPublicKey(cert.signatureKey)
		if err != nil {
			return err
		}
		var hasher crypto.Hash
		switch cert.sigFormat {
		case "rsa-sha2-256":
			hasher = crypto.SHA256
		case "rsa-sha2-512":
			hasher = crypto.SHA512
		case "ssh-rsa":
			hasher = crypto.SHA1
		default:
			return fmt.Errorf("%w: RSA signature format %q", errUnsupported, cert.sigFormat)
		}
		digest, err := hashBlob(hasher, cert.signed)
		if err != nil {
			return err
		}
		if rsa.VerifyPKCS1v15(pub, hasher, digest, cert.sigBlob) != nil {
			return errBadSignature
		}
		return nil

	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		pub, hasher, err := parseECDSAPublicKey(cert.signatureKey)
		if err != nil {
			return err
		}
		sc := &cursor{data: cert.sigBlob}
		rawR, err := sc.readString()
		if err != nil {
			return err
		}
		rawS, err := sc.readString()
		if err != nil {
			return err
		}
		digest, err := hashBlob(hasher, cert.signed)
		if err != nil {
			return err
		}
		if !ecdsa.Verify(pub, digest, new(big.Int).SetBytes(rawR), new(big.Int).SetBytes(rawS)) {
			return errBadSignature
		}
		return nil

	default:
		return fmt.Errorf("%w: CA key type %q", errUnsupported, caType)
	}
}

func parseRSAPublicKey(blob []byte) (*rsa.PublicKey, error) {
	c := &cursor{data: blob}
	if _, err := c.readString(); err != nil {
		return nil, err
	}
	rawE, err := c.readString()
	if err != nil {
		return nil, err
	}
	rawN, err := c.readString()
	if err != nil {
		return nil, err
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(rawN),
		E: int(new(big.Int).SetBytes(rawE).Int64()),
	}, nil
}

func parseECDSAPublicKey(blob []byte) (*ecdsa.PublicKey, crypto.Hash, error) {
	c := &cursor{data: blob}
	if _, err := c.readString(); err != nil { // key type
		return nil, 0, errShortBuffer
	}
	curveName, err := c.readString()
	if err != nil {
		return nil, 0, err
	}
	point, err := c.readString()
	if err != nil {
		return nil, 0, err
	}

	var curve elliptic.Curve
	var hasher crypto.Hash
	switch string(curveName) {
	case "nistp256":
		curve, hasher = elliptic.P256(), crypto.SHA256
	case "nistp384":
		curve, hasher = elliptic.P384(), crypto.SHA384
	case "nistp521":
		curve, hasher = elliptic.P521(), crypto.SHA512
	default:
		return nil, 0, fmt.Errorf("%w: ECDSA curve %q", errUnsupported, curveName)
	}

	x, y := elliptic.UnmarshalCompressed(curve, point)
	if x == nil {
		x, y = elliptic.Unmarshal(curve, point)
	}
	if x == nil {
		return nil, 0, errors.New("invalid ECDSA public point")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, hasher, nil
}

func hashBlob(hasher crypto.Hash, data []byte) ([]byte, error) {
	var h hash.Hash
	switch hasher {
	case crypto.SHA1:
		h = sha1.New()
	case crypto.SHA256:
		h = sha256.New()
	case crypto.SHA512:
		h = sha512.New()
	case crypto.SHA384:
		h = sha512.New384()
	default:
		return nil, fmt.Errorf("%w: hash %d", errUnsupported, hasher)
	}
	h.Write(data)
	return h.Sum(nil), nil
}
