// Package netsec implements certificate transparency (RFC 6962) checks for
// X.509 certificates presented during a TLS handshake.
//
// A final certificate carries the signed certificate timestamps (SCTs) in the
// "SCT list" extension (OID 1.3.6.1.4.1.11129.2.4.2). Every SCT was issued by a
// CT log over the certificate's *precertificate* TBS: the final certificate's
// TBS with the SCT list extension removed and the critical poison extension
// (OID 1.3.6.1.4.1.11129.2.4.3) added. This package rebuilds that TBS before
// verifying any SCT so that neither a precertificate presented as a final
// certificate nor an SCT list extension dragged into the signed digest can be
// accepted.
package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"time"
)

var (
	oidExtensionPoison  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 3}
	oidExtensionSCTList = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 4, 2}
)

const (
	sctVersionV1                      = 0
	sigTypeCertificateTimestamp       = 0
	logEntryTypePrecert               = 1
	hashAlgSHA256               uint8 = 4
	hashAlgSHA384               uint8 = 5
	sigAlgRSA                   uint8 = 1
	sigAlgECDSA                 uint8 = 3
)

// Verification errors. Callers can use errors.Is to match them.
var (
	ErrMissingCertificate      = errors.New("ct: no certificate to verify")
	ErrMissingIssuer           = errors.New("ct: issuer certificate is required")
	ErrPoisonInFinal           = errors.New("ct: final certificate must not contain the precertificate poison extension")
	ErrMissingPoison           = errors.New("ct: precertificate must contain the poison extension")
	ErrInvalidPoison           = errors.New("ct: poison extension must be critical with a NULL value")
	ErrNoSCTs                  = errors.New("ct: certificate carries no signed certificate timestamps")
	ErrMalformedSCT            = errors.New("ct: malformed signed certificate timestamp")
	ErrMalformedTBS            = errors.New("ct: malformed TBS certificate")
	ErrUnsupportedSCTVersion   = errors.New("ct: unsupported SCT version")
	ErrUnsupportedSigAlgorithm = errors.New("ct: unsupported SCT signature algorithm")
	ErrUnknownLog              = errors.New("ct: SCT was issued by an unknown CT log")
	ErrSCTInFuture             = errors.New("ct: SCT timestamp is later than verification time")
	ErrSCTSignature            = errors.New("ct: SCT signature does not verify")
)

// CTLog is a trusted certificate transparency log identified by the SHA-256
// hash of its SubjectPublicKeyInfo.
type CTLog struct {
	pub crypto.PublicKey
	id  [32]byte
}

// NewCTLog registers a CT log public key. RSA and ECDSA keys are supported.
func NewCTLog(pub crypto.PublicKey) (*CTLog, error) {
	switch pub.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey:
	default:
		return nil, fmt.Errorf("ct: unsupported CT log key type %T", pub)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("ct: cannot marshal CT log public key: %w", err)
	}
	return &CTLog{pub: pub, id: sha256.Sum256(der)}, nil
}

// LogID returns the RFC 6962 log identifier: SHA-256 of the log's
// SubjectPublicKeyInfo.
func (l *CTLog) LogID() [32]byte {
	return l.id
}

// LogStore holds the CT logs trusted for verification.
type LogStore struct {
	logs map[[32]byte]*CTLog
}

// NewLogStore builds a store containing the given logs.
func NewLogStore(logs ...*CTLog) *LogStore {
	s := &LogStore{logs: make(map[[32]byte]*CTLog)}
	for _, log := range logs {
		s.Add(log)
	}
	return s
}

// Add registers an additional CT log.
func (s *LogStore) Add(log *CTLog) {
	s.logs[log.id] = log
}

// VerifyOptions controls verification. The zero value uses the current time.
type VerifyOptions struct {
	Now time.Time
}

func (s *LogStore) resolveNow(opts *VerifyOptions) time.Time {
	if opts != nil && !opts.Now.IsZero() {
		return opts.Now
	}
	return time.Now()
}

// parsedSCT holds the fields of a v1 SerializedSCT (RFC 6962 section 3.2).
type parsedSCT struct {
	version    uint8
	logID      [32]byte
	timestamp  uint64
	extensions []byte
	hashAlg    uint8
	sigAlg     uint8
	signature  []byte
}

type sctReader struct {
	b   []byte
	pos int
}

func (r *sctReader) readByte() (byte, error) {
	b, err := r.read(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *sctReader) read(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, ErrMalformedSCT
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *sctReader) readUint16() (uint16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}

func (r *sctReader) readUint64() (uint64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

// parseSCTList parses the value of the SCT list extension: a uint16 total
// length followed by length-prefixed SerializedSCT structures.
func parseSCTList(extValue []byte) ([][]byte, error) {
	if len(extValue) < 2 {
		return nil, ErrMalformedSCT
	}
	total := int(binary.BigEndian.Uint16(extValue[:2]))
	if total != len(extValue)-2 {
		return nil, ErrMalformedSCT
	}
	rest := extValue[2:]
	var scts [][]byte
	for len(rest) > 0 {
		if len(rest) < 2 {
			return nil, ErrMalformedSCT
		}
		n := int(binary.BigEndian.Uint16(rest[:2]))
		rest = rest[2:]
		if n == 0 || n > len(rest) {
			return nil, ErrMalformedSCT
		}
		scts = append(scts, rest[:n])
		rest = rest[n:]
	}
	if len(scts) == 0 {
		return nil, ErrNoSCTs
	}
	return scts, nil
}

// parseSCT parses a single TLS-encoded SerializedSCT.
func parseSCT(raw []byte) (*parsedSCT, error) {
	r := &sctReader{b: raw}
	sct := &parsedSCT{}
	var err error
	if sct.version, err = r.readByte(); err != nil {
		return nil, err
	}
	id, err := r.read(32)
	if err != nil {
		return nil, err
	}
	copy(sct.logID[:], id)
	if sct.timestamp, err = r.readUint64(); err != nil {
		return nil, err
	}
	extLen, err := r.readUint16()
	if err != nil {
		return nil, err
	}
	if sct.extensions, err = r.read(int(extLen)); err != nil {
		return nil, err
	}
	if sct.hashAlg, err = r.readByte(); err != nil {
		return nil, err
	}
	if sct.sigAlg, err = r.readByte(); err != nil {
		return nil, err
	}
	sigLen, err := r.readUint16()
	if err != nil {
		return nil, err
	}
	if sct.signature, err = r.read(int(sigLen)); err != nil {
		return nil, err
	}
	if r.pos != len(raw) {
		return nil, ErrMalformedSCT
	}
	return sct, nil
}

// tbsCertificate mirrors crypto/x509's structure but keeps every other field
// as an opaque ASN.1 value so that re-marshalling preserves the original bytes
// exactly; only the extension list is rebuilt.
type tbsCertificate struct {
	Version         int `asn1:"optional,explicit,default:0,tag:0"`
	SerialNumber    asn1.RawValue
	Signature       asn1.RawValue
	Issuer          asn1.RawValue
	Validity        asn1.RawValue
	Subject         asn1.RawValue
	PublicKey       asn1.RawValue
	UniqueID        asn1.BitString   `asn1:"optional,tag:1"`
	SubjectUniqueID asn1.BitString   `asn1:"optional,tag:2"`
	Extensions      []pkix.Extension `asn1:"omitempty,explicit,tag:3"`
}

func findExtension(exts []pkix.Extension, oid asn1.ObjectIdentifier) (pkix.Extension, bool) {
	for _, ext := range exts {
		if ext.Id.Equal(oid) {
			return ext, true
		}
	}
	return pkix.Extension{}, false
}

func poisonExtension() pkix.Extension {
	return pkix.Extension{
		Id:       oidExtensionPoison,
		Critical: true,
		Value:    asn1.NullBytes,
	}
}

// validatePoison enforces RFC 6962 section 3.1: the poison extension must be
// present, critical, and carry an ASN.1 NULL value.
func validatePoison(cert *x509.Certificate) error {
	ext, ok := findExtension(cert.Extensions, oidExtensionPoison)
	if !ok {
		return ErrMissingPoison
	}
	if !ext.Critical || !bytes.Equal(ext.Value, asn1.NullBytes) {
		return ErrInvalidPoison
	}
	return nil
}

// rebuildPrecertTBS reconstructs the precertificate TBS that a CT log signed:
// the final certificate's TBS with the SCT list extension removed and the
// critical poison extension appended. Keeping the SCT list in the TBS would let
// an attacker choose SCTs after the fact while still satisfying the signature.
func rebuildPrecertTBS(finalTBS []byte) ([]byte, error) {
	var tbs tbsCertificate
	if _, err := asn1.Unmarshal(finalTBS, &tbs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedTBS, err)
	}
	kept := make([]pkix.Extension, 0, len(tbs.Extensions)+1)
	for _, ext := range tbs.Extensions {
		if ext.Id.Equal(oidExtensionSCTList) || ext.Id.Equal(oidExtensionPoison) {
			continue
		}
		kept = append(kept, ext)
	}
	kept = append(kept, poisonExtension())
	tbs.Extensions = kept
	der, err := asn1.Marshal(tbs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedTBS, err)
	}
	return der, nil
}

// stripSCTList returns the precertificate TBS without any SCT list extension.
func stripSCTList(precertTBS []byte) ([]byte, error) {
	var tbs tbsCertificate
	if _, err := asn1.Unmarshal(precertTBS, &tbs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedTBS, err)
	}
	var kept []pkix.Extension
	for _, ext := range tbs.Extensions {
		if ext.Id.Equal(oidExtensionSCTList) {
			continue
		}
		kept = append(kept, ext)
	}
	tbs.Extensions = kept
	der, err := asn1.Marshal(tbs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedTBS, err)
	}
	return der, nil
}

// buildPrecertSignedData assembles the TLS digitally-signed structure from
// RFC 6962 section 3.2 for a precertificate entry.
func buildPrecertSignedData(sct *parsedSCT, issuerKeyHash [32]byte, tbs []byte) ([]byte, error) {
	if len(tbs) > 0xFFFFFF {
		return nil, ErrMalformedTBS
	}
	var buf bytes.Buffer
	buf.WriteByte(sctVersionV1)
	buf.WriteByte(sigTypeCertificateTimestamp)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], sct.timestamp)
	buf.Write(tmp[:])
	binary.BigEndian.PutUint16(tmp[:2], logEntryTypePrecert)
	buf.Write(tmp[:2])
	buf.Write(issuerKeyHash[:])
	buf.Write([]byte{byte(len(tbs) >> 16), byte(len(tbs) >> 8), byte(len(tbs))})
	buf.Write(tbs)
	if len(sct.extensions) > 0xFFFF {
		return nil, ErrMalformedSCT
	}
	binary.BigEndian.PutUint16(tmp[:2], uint16(len(sct.extensions)))
	buf.Write(tmp[:2])
	buf.Write(sct.extensions)
	return buf.Bytes(), nil
}

func (l *CTLog) verify(signed []byte, sct *parsedSCT) error {
	var (
		hashID   crypto.Hash
		hashFunc func() hash.Hash
	)
	switch sct.hashAlg {
	case hashAlgSHA256:
		hashID, hashFunc = crypto.SHA256, sha256.New
	case hashAlgSHA384:
		hashID, hashFunc = crypto.SHA384, sha512.New384
	default:
		return ErrUnsupportedSigAlgorithm
	}
	h := hashFunc()
	h.Write(signed)
	digest := h.Sum(nil)

	switch sct.sigAlg {
	case sigAlgECDSA:
		pub, ok := l.pub.(*ecdsa.PublicKey)
		if !ok {
			return ErrUnsupportedSigAlgorithm
		}
		if !ecdsa.VerifyASN1(pub, digest, sct.signature) {
			return ErrSCTSignature
		}
	case sigAlgRSA:
		pub, ok := l.pub.(*rsa.PublicKey)
		if !ok {
			return ErrUnsupportedSigAlgorithm
		}
		if err := rsa.VerifyPKCS1v15(pub, hashID, digest, sct.signature); err != nil {
			return ErrSCTSignature
		}
	default:
		return ErrUnsupportedSigAlgorithm
	}
	return nil
}

// verifySCTs validates every SCT against the rebuilt precertificate TBS and
// the actual issuer's public key hash.
func (s *LogStore) verifySCTs(rawSCTs [][]byte, issuer *x509.Certificate, tbs []byte, now time.Time) error {
	issuerKeyHash := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	for i, raw := range rawSCTs {
		sct, err := parseSCT(raw)
		if err != nil {
			return fmt.Errorf("ct: SCT %d: %w", i, err)
		}
		if sct.version != sctVersionV1 {
			return fmt.Errorf("ct: SCT %d: %w", i, ErrUnsupportedSCTVersion)
		}
		// The timestamp is milliseconds since the Unix epoch; it must not be
		// later than the verification time.
		sctTime := time.UnixMilli(int64(sct.timestamp)).UTC()
		if sctTime.After(now) {
			return fmt.Errorf("ct: SCT %d: %w", i, ErrSCTInFuture)
		}
		log, ok := s.logs[sct.logID]
		if !ok {
			return fmt.Errorf("ct: SCT %d: %w", i, ErrUnknownLog)
		}
		signed, err := buildPrecertSignedData(sct, issuerKeyHash, tbs)
		if err != nil {
			return fmt.Errorf("ct: SCT %d: %w", i, err)
		}
		if err := log.verify(signed, sct); err != nil {
			return fmt.Errorf("ct: SCT %d: %w", i, err)
		}
	}
	return nil
}

// VerifyEmbeddedSCTs checks the SCTs embedded in a final leaf certificate.
//
// The leaf must not carry the precertificate poison extension; presenting a
// precertificate as a final certificate is rejected. The precertificate TBS is
// rebuilt by stripping the SCT list extension and adding poison, then every
// SCT is verified against the public key hash of the actual issuer.
func (s *LogStore) VerifyEmbeddedSCTs(leaf, issuer *x509.Certificate, opts *VerifyOptions) error {
	if leaf == nil {
		return ErrMissingCertificate
	}
	if issuer == nil {
		return ErrMissingIssuer
	}
	if _, poisoned := findExtension(leaf.Extensions, oidExtensionPoison); poisoned {
		return ErrPoisonInFinal
	}
	sctExt, ok := findExtension(leaf.Extensions, oidExtensionSCTList)
	if !ok {
		return ErrNoSCTs
	}
	rawSCTs, err := parseSCTList(sctExt.Value)
	if err != nil {
		return err
	}
	tbs, err := rebuildPrecertTBS(leaf.RawTBSCertificate)
	if err != nil {
		return err
	}
	return s.verifySCTs(rawSCTs, issuer, tbs, s.resolveNow(opts))
}

// VerifyPrecertificateSCTs checks SCTs issued for a real precertificate. The
// precertificate must contain a valid critical poison extension and the SCT
// list is never part of the signed TBS.
func (s *LogStore) VerifyPrecertificateSCTs(precert, issuer *x509.Certificate, rawSCTs [][]byte, opts *VerifyOptions) error {
	if precert == nil {
		return ErrMissingCertificate
	}
	if issuer == nil {
		return ErrMissingIssuer
	}
	if err := validatePoison(precert); err != nil {
		return err
	}
	if len(rawSCTs) == 0 {
		return ErrNoSCTs
	}
	tbs, err := stripSCTList(precert.RawTBSCertificate)
	if err != nil {
		return err
	}
	return s.verifySCTs(rawSCTs, issuer, tbs, s.resolveNow(opts))
}

// VerifyConnectionAt is a tls.Config.VerifyConnection callback that verifies
// the leaf certificate's embedded SCTs after the normal chain verification has
// run. The chain built by TLS verification is preferred so that the issuer key
// hash comes from the actual issuer; verifyTime makes the check deterministic.
func (s *LogStore) VerifyConnectionAt(cs tls.ConnectionState, verifyTime time.Time) error {
	if len(cs.PeerCertificates) == 0 {
		return ErrMissingCertificate
	}
	leaf := cs.PeerCertificates[0]
	issuer := s.pickIssuer(cs, leaf)
	if issuer == nil {
		return ErrMissingIssuer
	}
	return s.VerifyEmbeddedSCTs(leaf, issuer, &VerifyOptions{Now: verifyTime})
}

// VerifyConnection is the tls.Config.VerifyConnection callback using the
// current time for the timestamp check.
func (s *LogStore) VerifyConnection(cs tls.ConnectionState) error {
	return s.VerifyConnectionAt(cs, time.Now())
}

func (s *LogStore) pickIssuer(cs tls.ConnectionState, leaf *x509.Certificate) *x509.Certificate {
	if len(cs.VerifiedChains) > 0 && len(cs.VerifiedChains[0]) > 1 {
		return cs.VerifiedChains[0][1]
	}
	if len(cs.PeerCertificates) > 1 {
		return cs.PeerCertificates[1]
	}
	if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
		return leaf
	}
	return nil
}
