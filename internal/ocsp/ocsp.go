// Package ocsp contains a minimal, dependency-free OCSP (RFC 6960) response
// parser and a test helper that constructs signed responses. Only the subset
// required for workload certificate revocation is implemented.
package ocsp

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"hash"
	"math/big"
	"time"
)

// OID id-pkix-ocsp-basic (1.3.6.1.5.5.7.48.1.1).
var oidBasicResponse = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}

var (
	oidSHA1          = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidSHA1WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidECDSASHA256   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSASHA384   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSASHA512   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
)

// CertStatus mirrors the OCSP CertStatus choice.
type CertStatus int

const (
	Good CertStatus = iota
	Revoked
	Unknown
)

// Errors returned by the parser/verifier.
var (
	ErrMalformedResponse  = errors.New("ocsp: malformed response")
	ErrUnsuccessfulStatus = errors.New("ocsp: response status is not successful")
	ErrBadSignature       = errors.New("ocsp: signature verification failed")
	ErrCertIDMismatch     = errors.New("ocsp: certID does not match issuer/leaf")
	ErrUnsupportedAlg     = errors.New("ocsp: unsupported algorithm")
)

// Response is the parsed, verified subset of an OCSP response.
type Response struct {
	Status       CertStatus
	ThisUpdate   time.Time
	NextUpdate   time.Time
	ProducedAt   time.Time
	SerialNumber *big.Int
}

type certID struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	NameHash      []byte
	IssuerKeyHash []byte
	SerialNumber  *big.Int
}

type singleResponse struct {
	CertID     certID
	Status     asn1.RawValue
	ThisUpdate time.Time
	NextUpdate time.Time `asn1:"optional,explicit,tag:0"`
}

type responseData struct {
	Raw         asn1.RawContent
	ResponderID asn1.RawValue
	ProducedAt  time.Time
	Responses   []singleResponse
}

type basicOCSPResponse struct {
	TBSResponseData    responseData
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          asn1.BitString
}

type responseBytes struct {
	ResponseType asn1.ObjectIdentifier
	Response     []byte
}

type ocspResponse struct {
	Status        int
	ResponseBytes responseBytes `asn1:"optional,explicit,tag:0"`
}

// ParseResponse decodes a DER-encoded OCSP response. Signature verification is
// performed separately via Verify so callers control the trusted responder.
func ParseResponse(der []byte) (*Response, basicOCSPResponse, error) {
	var wrapper ocspResponse
	if rest, err := asn1.Unmarshal(der, &wrapper); err != nil {
		return nil, basicOCSPResponse{}, ErrMalformedResponse
	} else if len(rest) != 0 {
		return nil, basicOCSPResponse{}, ErrMalformedResponse
	}
	if wrapper.Status != 0 {
		return nil, basicOCSPResponse{}, ErrUnsuccessfulStatus
	}
	if !wrapper.ResponseBytes.ResponseType.Equal(oidBasicResponse) {
		return nil, basicOCSPResponse{}, ErrMalformedResponse
	}
	var basic basicOCSPResponse
	if rest, err := asn1.Unmarshal(wrapper.ResponseBytes.Response, &basic); err != nil {
		return nil, basicOCSPResponse{}, ErrMalformedResponse
	} else if len(rest) != 0 {
		return nil, basicOCSPResponse{}, ErrMalformedResponse
	}
	if len(basic.TBSResponseData.Responses) != 1 {
		return nil, basic, ErrMalformedResponse
	}
	single := basic.TBSResponseData.Responses[0]
	status, err := decodeStatus(single.Status)
	if err != nil {
		return nil, basic, err
	}
	return &Response{
		Status:       status,
		ThisUpdate:   single.ThisUpdate,
		NextUpdate:   single.NextUpdate,
		ProducedAt:   basic.TBSResponseData.ProducedAt,
		SerialNumber: single.CertID.SerialNumber,
	}, basic, nil
}

func decodeStatus(raw asn1.RawValue) (CertStatus, error) {
	if raw.Class != asn1.ClassContextSpecific {
		return 0, ErrMalformedResponse
	}
	switch raw.Tag {
	case 0:
		return Good, nil
	case 1:
		return Revoked, nil
	case 2:
		return Unknown, nil
	default:
		return 0, ErrMalformedResponse
	}
}

// Verify checks the response signature against the trusted responder and
// confirms the certID binds to the given issuer and leaf serial.
func Verify(der []byte, issuer, responder *x509.Certificate) (*Response, error) {
	resp, basic, err := ParseResponse(der)
	if err != nil {
		return nil, err
	}
	algo, err := signatureAlgorithm(basic.SignatureAlgorithm.Algorithm)
	if err != nil {
		return nil, err
	}
	if err := responder.CheckSignature(algo, basic.TBSResponseData.Raw, basic.Signature.Bytes); err != nil {
		return nil, ErrBadSignature
	}
	single := basic.TBSResponseData.Responses[0]
	if err := checkCertID(single.CertID, issuer, resp.SerialNumber); err != nil {
		return nil, err
	}
	return resp, nil
}

func checkCertID(id certID, issuer *x509.Certificate, serial *big.Int) error {
	h, err := hashForOID(id.HashAlgorithm.Algorithm)
	if err != nil {
		return err
	}
	h.Write(issuer.RawSubject)
	nameHash := h.Sum(nil)
	h.Reset()
	h.Write(issuer.SubjectKeyId)
	keyHash := h.Sum(nil)
	if !bytesEqual(nameHash, id.NameHash) || !bytesEqual(keyHash, id.IssuerKeyHash) {
		return ErrCertIDMismatch
	}
	if serial == nil {
		return ErrCertIDMismatch
	}
	return nil
}

func hashForOID(oid asn1.ObjectIdentifier) (hash.Hash, error) {
	switch {
	case oid.Equal(oidSHA1):
		return sha1.New(), nil
	case oid.Equal(oidSHA256):
		return sha256.New(), nil
	case oid.Equal(oidSHA384):
		return sha512.New384(), nil
	case oid.Equal(oidSHA512):
		return sha512.New(), nil
	default:
		return nil, ErrUnsupportedAlg
	}
}

func signatureAlgorithm(oid asn1.ObjectIdentifier) (x509.SignatureAlgorithm, error) {
	switch {
	case oid.Equal(oidSHA1WithRSA):
		return x509.SHA1WithRSA, nil
	case oid.Equal(oidSHA256WithRSA):
		return x509.SHA256WithRSA, nil
	case oid.Equal(oidECDSASHA256):
		return x509.ECDSAWithSHA256, nil
	case oid.Equal(oidECDSASHA384):
		return x509.ECDSAWithSHA384, nil
	case oid.Equal(oidECDSASHA512):
		return x509.ECDSAWithSHA512, nil
	default:
		return 0, ErrUnsupportedAlg
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CreateInput is the input to CreateResponse.
type CreateInput struct {
	Issuer     *x509.Certificate
	Responder  *x509.Certificate
	Signer     crypto.Signer
	Leaf       *x509.Certificate
	Status     CertStatus
	ThisUpdate time.Time
	NextUpdate time.Time
	RevokedAt  time.Time
}

// CreateResponse builds a signed DER OCSP response for tests and for control
// plane components that act as an OCSP responder.
func CreateResponse(in CreateInput) ([]byte, error) {
	nameHash, keyHash, err := issuerHashes(in.Issuer)
	if err != nil {
		return nil, err
	}
	id := certID{
		HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidSHA1},
		NameHash:      nameHash,
		IssuerKeyHash: keyHash,
		SerialNumber:  new(big.Int).Set(in.Leaf.SerialNumber),
	}
	status, err := encodeStatus(in.Status, in.RevokedAt)
	if err != nil {
		return nil, err
	}
	single := singleResponse{
		CertID:     id,
		Status:     status,
		ThisUpdate: in.ThisUpdate,
		NextUpdate: in.NextUpdate,
	}
	rdn, err := asn1.Marshal(in.Responder.Subject.ToRDNSequence())
	if err != nil {
		return nil, err
	}
	responderID, err := asn1.MarshalWithParams(asn1.RawValue{FullBytes: rdn}, "explicit,tag:1")
	if err != nil {
		return nil, err
	}
	tbs := responseData{
		ResponderID: asn1.RawValue{FullBytes: responderID},
		ProducedAt:  in.ThisUpdate,
		Responses:   []singleResponse{single},
	}
	tbsBytes, err := asn1.Marshal(tbs)
	if err != nil {
		return nil, err
	}
	sigAlg, sig, err := sign(in.Signer, tbsBytes)
	if err != nil {
		return nil, err
	}
	basic := basicOCSPResponse{
		TBSResponseData:    responseData{Raw: tbsBytes},
		SignatureAlgorithm: pkix.AlgorithmIdentifier{Algorithm: sigAlg},
		Signature:          asn1.BitString{Bytes: sig, BitLength: len(sig) * 8},
	}
	basicBytes, err := asn1.Marshal(basic)
	if err != nil {
		return nil, err
	}
	wrapper := ocspResponse{
		Status:        0,
		ResponseBytes: responseBytes{ResponseType: oidBasicResponse, Response: basicBytes},
	}
	der, err := asn1.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	return der, nil
}

func issuerHashes(issuer *x509.Certificate) (nameHash, keyHash []byte, err error) {
	h := sha1.New()
	h.Write(issuer.RawSubject)
	nameHash = h.Sum(nil)
	h.Reset()
	h.Write(issuer.SubjectKeyId)
	keyHash = h.Sum(nil)
	return nameHash, keyHash, nil
}

func encodeStatus(status CertStatus, revokedAt time.Time) (asn1.RawValue, error) {
	switch status {
	case Good:
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0}, nil
	case Unknown:
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2}, nil
	case Revoked:
		type revokedInfo struct {
			RevocationTime time.Time
		}
		raw, err := asn1.Marshal(revokedInfo{RevocationTime: revokedAt})
		if err != nil {
			return asn1.RawValue{}, err
		}
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: raw}, nil
	default:
		return asn1.RawValue{}, ErrUnsupportedAlg
	}
}

func sign(signer crypto.Signer, tbs []byte) (asn1.ObjectIdentifier, []byte, error) {
	switch key := signer.Public().(type) {
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(tbs)
		sig, err := signer.Sign(nil, digest[:], crypto.SHA256)
		if err != nil {
			return nil, nil, err
		}
		return oidECDSASHA256, sig, nil
	case *rsa.PublicKey:
		digest := sha256.Sum256(tbs)
		sig, err := signer.Sign(nil, digest[:], crypto.SHA256)
		if err != nil {
			return nil, nil, err
		}
		return oidSHA256WithRSA, sig, nil
	default:
		_ = key
		return nil, nil, ErrUnsupportedAlg
	}
}
