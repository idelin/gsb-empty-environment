package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// RRSIG is a DNSSEC signature record. SignerName and the owner name of the
// covered RRset are both the child zone apex in this package.
type RRSIG struct {
	TypeCovered uint16
	Algorithm   uint8
	Labels      uint8
	OriginalTTL uint32
	Expiration  uint32
	Inception   uint32
	KeyTag      uint16
	SignerName  string
	Signature   []byte
}

// String returns a compact description useful in errors and logs.
func (s RRSIG) String() string {
	return fmt.Sprintf("RRSIG(type=%d alg=%d keytag=%d signer=%s)", s.TypeCovered, s.Algorithm, s.KeyTag, s.SignerName)
}

// VerifyRRSet verifies that at least one signature in sigs is a valid
// signature by key over the canonical RRset consisting of records.
//
// records contains RDATA in wire format. zone is the owner name of the RRset
// and must also be the RRSIG signer name.
func VerifyRRSet(zone string, typ uint16, records [][]byte, sigs []RRSIG, key DNSKEY, now time.Time) error {
	if len(sigs) == 0 {
		return errors.New("RRset has no RRSIG")
	}
	var lastErr error
	for _, sig := range sigs {
		if err := verifyOneRRSIG(zone, typ, records, sig, key, now); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable RRSIG")
	}
	return fmt.Errorf("no valid RRSIG: %w", lastErr)
}

func verifyOneRRSIG(zone string, typ uint16, records [][]byte, sig RRSIG, key DNSKEY, now time.Time) error {
	if sig.TypeCovered != typ {
		return fmt.Errorf("%s covers type %d, want %d", sig, sig.TypeCovered, typ)
	}
	if sig.Algorithm != key.Algorithm {
		return fmt.Errorf("%s algorithm does not match DNSKEY algorithm %d", sig, key.Algorithm)
	}
	if sig.KeyTag != key.KeyTag() {
		return fmt.Errorf("%s key tag does not match DNSKEY key tag %d", sig, key.KeyTag())
	}
	if err := checkSignatureTime(now, sig.Inception, sig.Expiration); err != nil {
		return err
	}
	data, err := rrsetSigningData(zone, typ, records, sig)
	if err != nil {
		return err
	}
	return verifySignature(key, data, sig.Signature)
}

func checkSignatureTime(now time.Time, inception, expiration uint32) error {
	current := uint32(now.Unix())
	if !serialLE(inception, current) {
		return fmt.Errorf("RRSIG is not valid before %d", inception)
	}
	if !serialLE(current, expiration) {
		return fmt.Errorf("RRSIG expired at %d", expiration)
	}
	return nil
}

func serialLE(a, b uint32) bool {
	return int32(a-b) <= 0
}

func rrsetSigningData(zone string, typ uint16, records [][]byte, sig RRSIG) ([]byte, error) {
	canonicalZone, err := canonicalName(zone)
	if err != nil {
		return nil, err
	}
	ownerWire, err := nameWire(canonicalZone)
	if err != nil {
		return nil, err
	}
	signerWire, err := nameWire(sig.SignerName)
	if err != nil {
		return nil, fmt.Errorf("invalid RRSIG signer name: %w", err)
	}
	if !bytes.Equal(ownerWire, signerWire) {
		return nil, fmt.Errorf("RRSIG signer %q does not match RRset owner %q", sig.SignerName, canonicalZone)
	}
	labels, err := labelCount(canonicalZone)
	if err != nil {
		return nil, err
	}
	if sig.Labels != labels {
		return nil, fmt.Errorf("RRSIG labels %d does not match owner %q (%d labels)", sig.Labels, canonicalZone, labels)
	}
	if len(records) == 0 {
		return nil, errors.New("cannot sign an empty RRset")
	}

	sorted := canonicalRData(records)
	var buf bytes.Buffer

	writeUint16(&buf, sig.TypeCovered)
	buf.WriteByte(sig.Algorithm)
	buf.WriteByte(sig.Labels)
	writeUint32(&buf, sig.OriginalTTL)
	writeUint32(&buf, sig.Expiration)
	writeUint32(&buf, sig.Inception)
	writeUint16(&buf, sig.KeyTag)
	buf.Write(signerWire)

	for _, rdata := range sorted {
		buf.Write(ownerWire)
		writeUint16(&buf, typ)
		writeUint16(&buf, ClassINET)
		writeUint32(&buf, sig.OriginalTTL)
		writeUint16(&buf, uint16(len(rdata)))
		buf.Write(rdata)
	}
	return buf.Bytes(), nil
}

func canonicalRData(records [][]byte) [][]byte {
	unique := make(map[string][]byte, len(records))
	for _, rdata := range records {
		copyOfRData := append([]byte(nil), rdata...)
		unique[string(copyOfRData)] = copyOfRData
	}
	out := make([][]byte, 0, len(unique))
	for _, rdata := range unique {
		out = append(out, rdata)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i], out[j]) < 0
	})
	return out
}

func writeUint16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func verifySignature(key DNSKEY, data, signature []byte) error {
	switch key.Algorithm {
	case AlgorithmRSASHA1, AlgorithmRSASHA1NSEC3, AlgorithmRSASHA256, AlgorithmRSASHA512:
		pub, err := rsaPublicKey(key)
		if err != nil {
			return err
		}
		hash, err := hashForAlgorithm(key.Algorithm)
		if err != nil {
			return err
		}
		digest := hashData(hash, data)
		if err := rsa.VerifyPKCS1v15(pub, hash, digest, signature); err != nil {
			return fmt.Errorf("RSA signature verification failed: %w", err)
		}
		return nil

	case AlgorithmECDSAP256SHA256, AlgorithmECDSAP384SHA384:
		pub, err := ecdsaPublicKey(key)
		if err != nil {
			return err
		}
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return fmt.Errorf("ECDSA signature has length %d, want %d", len(signature), 2*size)
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		hash, err := hashForAlgorithm(key.Algorithm)
		if err != nil {
			return err
		}
		digest := hashData(hash, data)
		if !ecdsa.Verify(pub, digest, r, s) {
			return errors.New("ECDSA signature verification failed")
		}
		return nil

	case AlgorithmED25519:
		if len(key.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("Ed25519 public key has length %d, want %d", len(key.PublicKey), ed25519.PublicKeySize)
		}
		if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), data, signature) {
			return errors.New("Ed25519 signature verification failed")
		}
		return nil

	default:
		return fmt.Errorf("unsupported DNSSEC algorithm %d", key.Algorithm)
	}
}

func hashForAlgorithm(algorithm uint8) (crypto.Hash, error) {
	switch algorithm {
	case AlgorithmRSASHA1, AlgorithmRSASHA1NSEC3:
		return crypto.SHA1, nil
	case AlgorithmRSASHA256, AlgorithmECDSAP256SHA256:
		return crypto.SHA256, nil
	case AlgorithmRSASHA512:
		return crypto.SHA512, nil
	case AlgorithmECDSAP384SHA384:
		return crypto.SHA384, nil
	default:
		return 0, fmt.Errorf("unsupported DNSSEC algorithm %d", algorithm)
	}
}

func hashData(hash crypto.Hash, data []byte) []byte {
	switch hash {
	case crypto.SHA1:
		sum := sha1.Sum(data)
		return sum[:]
	case crypto.SHA256:
		sum := sha256.Sum256(data)
		return sum[:]
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		return nil
	}
}

func rsaPublicKey(key DNSKEY) (*rsa.PublicKey, error) {
	b := key.PublicKey
	if len(b) < 3 {
		return nil, errors.New("RSA public key is too short")
	}
	offset := 1
	exponentLength := int(b[0])
	if exponentLength == 0 {
		if len(b) < 3 {
			return nil, errors.New("RSA public key has a truncated exponent length")
		}
		exponentLength = int(binary.BigEndian.Uint16(b[1:3]))
		offset = 3
	}
	if exponentLength == 0 {
		return nil, errors.New("RSA public key has an empty exponent")
	}
	if offset+exponentLength >= len(b) {
		return nil, errors.New("RSA public key is missing its modulus")
	}
	exponent := new(big.Int).SetBytes(b[offset : offset+exponentLength])
	if !exponent.IsInt64() {
		return nil, errors.New("RSA exponent is too large")
	}
	e := exponent.Int64()
	if e < 3 || e > int64(^uint32(0)>>1) {
		return nil, fmt.Errorf("invalid RSA exponent %d", e)
	}
	modulus := new(big.Int).SetBytes(b[offset+exponentLength:])
	if modulus.Sign() <= 0 {
		return nil, errors.New("RSA modulus is empty")
	}
	return &rsa.PublicKey{N: modulus, E: int(e)}, nil
}

func ecdsaPublicKey(key DNSKEY) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch key.Algorithm {
	case AlgorithmECDSAP256SHA256:
		curve = elliptic.P256()
	case AlgorithmECDSAP384SHA384:
		curve = elliptic.P384()
	default:
		return nil, fmt.Errorf("unsupported ECDSA algorithm %d", key.Algorithm)
	}
	size := (curve.Params().BitSize + 7) / 8
	if len(key.PublicKey) != 2*size {
		return nil, fmt.Errorf("ECDSA public key has length %d, want %d", len(key.PublicKey), 2*size)
	}
	x := new(big.Int).SetBytes(key.PublicKey[:size])
	y := new(big.Int).SetBytes(key.PublicKey[size:])
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("ECDSA public key is not on the curve")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// SignRRSet creates a DNSSEC RRSIG over records for tooling and tests. The
// acceptance path itself never needs private key material.
func SignRRSet(zone string, typ uint16, records [][]byte, privateKey crypto.Signer, key DNSKEY, inception, expiration time.Time, ttl uint32) (RRSIG, error) {
	if err := key.Validate(); err != nil {
		return RRSIG{}, err
	}
	labels, err := labelCount(zone)
	if err != nil {
		return RRSIG{}, err
	}
	canonicalZone, err := canonicalName(zone)
	if err != nil {
		return RRSIG{}, err
	}
	sig := RRSIG{
		TypeCovered: typ,
		Algorithm:   key.Algorithm,
		Labels:      labels,
		OriginalTTL: ttl,
		Expiration:  uint32(expiration.Unix()),
		Inception:   uint32(inception.Unix()),
		KeyTag:      key.KeyTag(),
		SignerName:  canonicalZone,
	}
	data, err := rrsetSigningData(canonicalZone, typ, records, sig)
	if err != nil {
		return RRSIG{}, err
	}
	signature, err := signData(privateKey, key.Algorithm, data)
	if err != nil {
		return RRSIG{}, err
	}
	sig.Signature = signature
	return sig, nil
}

func signData(privateKey crypto.Signer, algorithm uint8, data []byte) ([]byte, error) {
	switch algorithm {
	case AlgorithmRSASHA1, AlgorithmRSASHA1NSEC3, AlgorithmRSASHA256, AlgorithmRSASHA512:
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return nil, err
		}
		digest := hashData(hash, data)
		return privateKey.Sign(rand.Reader, digest, hash)

	case AlgorithmECDSAP256SHA256, AlgorithmECDSAP384SHA384:
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return nil, err
		}
		digest := hashData(hash, data)
		der, err := privateKey.Sign(rand.Reader, digest, hash)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			R *big.Int
			S *big.Int
		}
		if _, err := asn1.Unmarshal(der, &parsed); err != nil {
			return nil, fmt.Errorf("decode ECDSA signature: %w", err)
		}
		curveSize := 32
		if algorithm == AlgorithmECDSAP384SHA384 {
			curveSize = 48
		}
		signature := make([]byte, 2*curveSize)
		parsed.R.FillBytes(signature[:curveSize])
		parsed.S.FillBytes(signature[curveSize:])
		return signature, nil

	case AlgorithmED25519:
		return privateKey.Sign(rand.Reader, data, crypto.Hash(0))

	default:
		return nil, fmt.Errorf("unsupported DNSSEC algorithm %d", algorithm)
	}
}
