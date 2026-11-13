package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"hash"
	"math/big"
	"time"
)

var (
	oidSHA1       = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidSHA512_224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 5}
	oidSHA512_256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 6}
	oidBasicOCSP  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}
)

type ocspResponse struct {
	ResponseStatus asn1.Enumerated
	ResponseBytes  asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type responseBytes struct {
	ResponseType asn1.ObjectIdentifier
	Response     []byte
}

type basicOCSPResponse struct {
	TBSResponseData    asn1.RawValue
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          asn1.BitString
	Certificates       []asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type responseData struct {
	Version     int `asn1:"explicit,optional,tag:0,default:0"`
	ResponderID asn1.RawValue
	ProducedAt  time.Time `asn1:"generalized"`
	Responses   []rawSingleResponse
	Extensions  []pkix.Extension `asn1:"explicit,optional,tag:1"`
}

type rawSingleResponse struct {
	CertID           rawCertID
	CertStatus       asn1.RawValue
	ThisUpdate       time.Time        `asn1:"generalized"`
	NextUpdate       time.Time        `asn1:"explicit,optional,tag:0,generalized"`
	SingleExtensions []pkix.Extension `asn1:"explicit,optional,tag:1"`
}

type rawCertID struct {
	HashAlgorithm  pkix.AlgorithmIdentifier
	IssuerNameHash []byte
	IssuerKeyHash  []byte
	SerialNumber   *big.Int
}

type revokedInfo struct {
	RevocationTime   time.Time       `asn1:"generalized"`
	RevocationReason asn1.Enumerated `asn1:"explicit,optional,tag:0"`
}

type subjectPublicKeyInfo struct {
	Algorithm        pkix.AlgorithmIdentifier
	SubjectPublicKey asn1.BitString
}

// ParseBasicResponse parses an OCSPResponse and returns its
// BasicOCSPResponse. It validates the ASN.1 structure and rejects duplicate
// SingleResponse entries for one CertID that disagree about the status. It
// does not verify the response signature or freshness.
func ParseBasicResponse(der []byte) (*BasicResponse, error) {
	var top ocspResponse
	rest, err := asn1.Unmarshal(der, &top)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", ErrMalformedResponse)
	}
	if int(top.ResponseStatus) != Good {
		return nil, fmt.Errorf("%w: status %d", ErrResponseNotSuccessful, int(top.ResponseStatus))
	}
	if top.ResponseBytes.FullBytes == nil {
		return nil, fmt.Errorf("%w: missing responseBytes", ErrMalformedResponse)
	}

	var bytesBlock responseBytes
	rest, err = asn1.Unmarshal(top.ResponseBytes.Bytes, &bytesBlock)
	if err != nil {
		return nil, fmt.Errorf("%w: responseBytes: %v", ErrMalformedResponse, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing responseBytes data", ErrMalformedResponse)
	}
	if !bytesBlock.ResponseType.Equal(oidBasicOCSP) {
		return nil, fmt.Errorf("%w: %v", ErrUnsupportedResponseType, bytesBlock.ResponseType)
	}

	var basic basicOCSPResponse
	rest, err = asn1.Unmarshal(bytesBlock.Response, &basic)
	if err != nil {
		return nil, fmt.Errorf("%w: basic response: %v", ErrMalformedResponse, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing basic response data", ErrMalformedResponse)
	}
	if basic.TBSResponseData.Class != asn1.ClassUniversal ||
		basic.TBSResponseData.Tag != asn1.TagSequence ||
		!basic.TBSResponseData.IsCompound {
		return nil, fmt.Errorf("%w: responseData is not a SEQUENCE", ErrMalformedResponse)
	}

	var data responseData
	rest, err = asn1.Unmarshal(basic.TBSResponseData.FullBytes, &data)
	if err != nil {
		return nil, fmt.Errorf("%w: responseData: %v", ErrMalformedResponse, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing responseData", ErrMalformedResponse)
	}
	if data.Version != 0 {
		return nil, fmt.Errorf("%w: unsupported response version %d", ErrMalformedResponse, data.Version)
	}

	responderID, err := parseResponderID(data.ResponderID)
	if err != nil {
		return nil, err
	}
	if len(data.Responses) == 0 {
		return nil, ErrNoSingleResponse
	}

	parsed := &BasicResponse{
		ProducedAt:         data.ProducedAt,
		ResponderID:        responderID,
		Raw:                append([]byte(nil), der...),
		tbsDER:             append([]byte(nil), basic.TBSResponseData.FullBytes...),
		signatureAlgorithm: basic.SignatureAlgorithm,
		signature:          basic.Signature,
	}

	for _, raw := range basic.Certificates {
		cert, err := x509.ParseCertificate(raw.FullBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: responder certificate: %v", ErrMalformedResponse, err)
		}
		parsed.Certificates = append(parsed.Certificates, cert)
	}

	for _, raw := range data.Responses {
		response, err := parseSingleResponse(raw)
		if err != nil {
			return nil, err
		}
		parsed.Responses = append(parsed.Responses, response)
	}
	if err := rejectConflictingStatuses(parsed.Responses); err != nil {
		return nil, err
	}
	return parsed, nil
}

func parseResponderID(raw asn1.RawValue) (ResponderID, error) {
	if raw.Class != asn1.ClassContextSpecific {
		return ResponderID{}, fmt.Errorf("%w: invalid responderID class", ErrMalformedResponse)
	}
	switch raw.Tag {
	case 1:
		if !raw.IsCompound || len(raw.Bytes) == 0 {
			return ResponderID{}, fmt.Errorf("%w: invalid byName responderID", ErrMalformedResponse)
		}
		var name asn1.RawValue
		rest, err := asn1.Unmarshal(raw.Bytes, &name)
		if err != nil || len(rest) != 0 || name.Tag != asn1.TagSequence {
			return ResponderID{}, fmt.Errorf("%w: invalid responder name", ErrMalformedResponse)
		}
		return ResponderID{ByName: append([]byte(nil), raw.Bytes...)}, nil
	case 2:
		if raw.IsCompound || len(raw.Bytes) == 0 {
			return ResponderID{}, fmt.Errorf("%w: invalid byKey responderID", ErrMalformedResponse)
		}
		return ResponderID{ByKey: append([]byte(nil), raw.Bytes...)}, nil
	default:
		return ResponderID{}, fmt.Errorf("%w: unknown responderID tag %d", ErrMalformedResponse, raw.Tag)
	}
}

func parseSingleResponse(raw rawSingleResponse) (SingleResponse, error) {
	certID, err := parseCertID(raw.CertID)
	if err != nil {
		return SingleResponse{}, err
	}

	status, revokedAt, reason, err := parseCertStatus(raw.CertStatus)
	if err != nil {
		return SingleResponse{}, err
	}
	if raw.ThisUpdate.IsZero() {
		return SingleResponse{}, fmt.Errorf("%w: missing thisUpdate", ErrMalformedResponse)
	}
	if !raw.NextUpdate.IsZero() && raw.NextUpdate.Before(raw.ThisUpdate) {
		return SingleResponse{}, fmt.Errorf("%w: nextUpdate precedes thisUpdate", ErrMalformedResponse)
	}

	return SingleResponse{
		CertID:           certID,
		Status:           status,
		ThisUpdate:       raw.ThisUpdate,
		NextUpdate:       raw.NextUpdate,
		RevokedAt:        revokedAt,
		RevocationReason: reason,
		Extensions:       append([]pkix.Extension(nil), raw.SingleExtensions...),
	}, nil
}

func parseCertID(raw rawCertID) (CertID, error) {
	if raw.SerialNumber == nil {
		return CertID{}, fmt.Errorf("%w: missing certificate serial number", ErrMalformedResponse)
	}
	hashID, _, err := hashForOID(raw.HashAlgorithm.Algorithm)
	if err != nil {
		return CertID{}, err
	}
	return CertID{
		HashAlgorithm:    hashID,
		HashAlgorithmOID: append(asn1.ObjectIdentifier(nil), raw.HashAlgorithm.Algorithm...),
		IssuerNameHash:   append([]byte(nil), raw.IssuerNameHash...),
		IssuerKeyHash:    append([]byte(nil), raw.IssuerKeyHash...),
		SerialNumber:     new(big.Int).Set(raw.SerialNumber),
	}, nil
}

func parseCertStatus(raw asn1.RawValue) (int, time.Time, int, error) {
	if raw.Class != asn1.ClassContextSpecific {
		return 0, time.Time{}, 0, fmt.Errorf("%w: invalid certStatus class", ErrMalformedResponse)
	}
	switch raw.Tag {
	case 0:
		if raw.IsCompound || len(raw.Bytes) != 0 {
			return 0, time.Time{}, 0, fmt.Errorf("%w: invalid good status", ErrMalformedResponse)
		}
		return Good, time.Time{}, 0, nil
	case 1:
		if !raw.IsCompound {
			return 0, time.Time{}, 0, fmt.Errorf("%w: invalid revoked status", ErrMalformedResponse)
		}
		infoDER := wrapSequence(raw.Bytes)
		var info revokedInfo
		rest, err := asn1.Unmarshal(infoDER, &info)
		if err != nil {
			return 0, time.Time{}, 0, fmt.Errorf("%w: revokedInfo: %v", ErrMalformedResponse, err)
		}
		if len(rest) != 0 || info.RevocationTime.IsZero() {
			return 0, time.Time{}, 0, fmt.Errorf("%w: invalid revokedInfo", ErrMalformedResponse)
		}
		return Revoked, info.RevocationTime, int(info.RevocationReason), nil
	case 2:
		if raw.IsCompound || len(raw.Bytes) != 0 {
			return 0, time.Time{}, 0, fmt.Errorf("%w: invalid unknown status", ErrMalformedResponse)
		}
		return Unknown, time.Time{}, 0, nil
	default:
		return 0, time.Time{}, 0, fmt.Errorf("%w: unknown certStatus tag %d", ErrMalformedResponse, raw.Tag)
	}
}

// ParseResponse parses an OCSP response without verifying its signature or
// freshness. If the response contains entries for exactly one certificate
// identity, that entry (including any duplicate entries) is returned. This
// mirrors the single-certificate response shape used by most callers while
// still rejecting conflicting duplicates.
//
// An optional issuer can be supplied for compatibility with callers that pass
// the issuer to parsing routines; it is not used for selection here.
func ParseResponse(der []byte, issuers ...*x509.Certificate) (*Response, error) {
	basic, err := ParseBasicResponse(der)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	for _, response := range basic.Responses {
		seen[certIDKey(response.CertID)] = struct{}{}
	}
	if len(seen) != 1 {
		return nil, fmt.Errorf("%w: response contains %d certificate identities; use ParseResponseForCert", ErrMalformedResponse, len(seen))
	}

	selected := append([]SingleResponse(nil), basic.Responses...)
	return responseForMatches(basic, selected, basic.Responses[0].CertID.SerialNumber)
}

// ParseResponseForCert parses an OCSP response and selects all SingleResponse
// entries for cert/issuer. It rejects conflicting statuses for the same
// certificate and never selects "good" over "revoked".
//
// This function performs parsing and selection only. Certificate path
// validation should use VerifyResponse or CheckStatus so the response
// signature and freshness are checked as well.
func ParseResponseForCert(der []byte, cert, issuer *x509.Certificate) (*Response, error) {
	if cert == nil || issuer == nil || cert.SerialNumber == nil {
		return nil, fmt.Errorf("%w: certificate and issuer are required", ErrMalformedResponse)
	}

	basic, err := ParseBasicResponse(der)
	if err != nil {
		return nil, err
	}
	matches, err := matchingResponses(basic.Responses, cert, issuer)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: serial %v", ErrCertificateNotFound, cert.SerialNumber)
	}
	return responseForMatches(basic, matches, cert.SerialNumber)
}

func matchingResponses(responses []SingleResponse, cert, issuer *x509.Certificate) ([]SingleResponse, error) {
	var matches []SingleResponse
	for _, response := range responses {
		match, err := certIDMatches(response.CertID, cert, issuer)
		if err != nil {
			return nil, err
		}
		if match {
			matches = append(matches, response)
		}
	}
	return matches, nil
}

func responseForMatches(basic *BasicResponse, matches []SingleResponse, serial *big.Int) (*Response, error) {
	if len(matches) == 0 {
		return nil, ErrCertificateNotFound
	}

	status := matches[0].Status
	for _, match := range matches[1:] {
		if match.Status != status {
			return nil, fmt.Errorf(
				"%w: serial %v has both %s and %s",
				ErrConflictingStatus,
				serial,
				StatusString(status),
				StatusString(match.Status),
			)
		}
	}

	result := &Response{
		Status:           status,
		SerialNumber:     new(big.Int).Set(serial),
		ProducedAt:       basic.ProducedAt,
		RevokedAt:        matches[0].RevokedAt,
		RevocationReason: matches[0].RevocationReason,
		Responses:        append([]SingleResponse(nil), matches...),
	}
	for _, match := range matches {
		if result.ThisUpdate.IsZero() || (!match.ThisUpdate.IsZero() && match.ThisUpdate.Before(result.ThisUpdate)) {
			result.ThisUpdate = match.ThisUpdate
		}
		if result.NextUpdate.IsZero() || (!match.NextUpdate.IsZero() && match.NextUpdate.Before(result.NextUpdate)) {
			result.NextUpdate = match.NextUpdate
		}
		if status == Revoked && !match.RevokedAt.IsZero() &&
			(result.RevokedAt.IsZero() || match.RevokedAt.Before(result.RevokedAt)) {
			result.RevokedAt = match.RevokedAt
		}
	}
	return result, nil
}

func rejectConflictingStatuses(responses []SingleResponse) error {
	statuses := make(map[string]int, len(responses))
	for _, response := range responses {
		key := certIDKey(response.CertID)
		if status, ok := statuses[key]; ok {
			if status != response.Status {
				return fmt.Errorf(
					"%w: serial %v has both %s and %s",
					ErrConflictingStatus,
					response.CertID.SerialNumber,
					StatusString(status),
					StatusString(response.Status),
				)
			}
			continue
		}
		statuses[key] = response.Status
	}
	return nil
}

func certIDKey(id CertID) string {
	serial := ""
	if id.SerialNumber != nil {
		serial = id.SerialNumber.String()
	}
	return fmt.Sprintf("%s|%x|%x|%s", id.HashAlgorithmOID, id.IssuerNameHash, id.IssuerKeyHash, serial)
}

func certIDMatches(id CertID, cert, issuer *x509.Certificate) (bool, error) {
	if cert == nil || issuer == nil {
		return false, fmt.Errorf("%w: certificate and issuer are required", ErrMalformedResponse)
	}
	if cert.SerialNumber == nil || id.SerialNumber == nil || id.SerialNumber.Cmp(cert.SerialNumber) != 0 {
		return false, nil
	}
	if _, _, err := hashForOID(id.HashAlgorithmOID); err != nil {
		return false, err
	}

	issuerNameHash, err := hashBytes(id.HashAlgorithmOID, issuer.RawSubject)
	if err != nil {
		return false, err
	}
	issuerKeyHash, err := issuerPublicKeyHash(id.HashAlgorithmOID, issuer)
	if err != nil {
		return false, err
	}
	return bytes.Equal(id.IssuerNameHash, issuerNameHash) &&
		bytes.Equal(id.IssuerKeyHash, issuerKeyHash), nil
}

func issuerPublicKeyHash(oid asn1.ObjectIdentifier, cert *x509.Certificate) ([]byte, error) {
	var spki subjectPublicKeyInfo
	rest, err := asn1.Unmarshal(cert.RawSubjectPublicKeyInfo, &spki)
	if err != nil || len(rest) != 0 {
		return nil, fmt.Errorf("%w: invalid issuer public key", ErrMalformedResponse)
	}
	return hashBytes(oid, spki.SubjectPublicKey.Bytes)
}

func hashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, func() hash.Hash, error) {
	switch {
	case oid.Equal(oidSHA1):
		return crypto.SHA1, sha1.New, nil
	case oid.Equal(oidSHA224):
		return crypto.SHA224, sha256.New224, nil
	case oid.Equal(oidSHA256):
		return crypto.SHA256, sha256.New, nil
	case oid.Equal(oidSHA384):
		return crypto.SHA384, sha512.New384, nil
	case oid.Equal(oidSHA512):
		return crypto.SHA512, sha512.New, nil
	case oid.Equal(oidSHA512_224):
		return crypto.SHA512_224, sha512.New512_224, nil
	case oid.Equal(oidSHA512_256):
		return crypto.SHA512_256, sha512.New512_256, nil
	default:
		return 0, nil, fmt.Errorf("%w: %v", ErrUnsupportedHashAlgorithm, oid)
	}
}

func hashBytes(oid asn1.ObjectIdentifier, data []byte) ([]byte, error) {
	_, newHash, err := hashForOID(oid)
	if err != nil {
		return nil, err
	}
	h := newHash()
	h.Write(data)
	return h.Sum(nil), nil
}

// wrapSequence turns the contents of an implicitly tagged SEQUENCE back into
// a complete DER SEQUENCE so encoding/asn1 can decode it.
func wrapSequence(contents []byte) []byte {
	result := make([]byte, 0, len(contents)+8)
	result = append(result, byte(asn1.TagSequence)|0x20)
	result = append(result, derLength(len(contents))...)
	result = append(result, contents...)
	return result
}

func derLength(n int) []byte {
	if n < 0 {
		panic("negative DER length")
	}
	if n < 0x80 {
		return []byte{byte(n)}
	}

	var lengthBytes []byte
	for n > 0 {
		lengthBytes = append([]byte{byte(n & 0xff)}, lengthBytes...)
		n >>= 8
	}
	return append([]byte{0x80 | byte(len(lengthBytes))}, lengthBytes...)
}
