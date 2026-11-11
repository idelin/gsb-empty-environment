package ocspcheck

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Aliases kept in a dedicated block so the ASN.1 structs read like RFC terms.
type (
	timeValue = time.Time
	bigInt    = big.Int
)

// OCSP response status values (RFC 6960 section 4.2.1).
const (
	statusSuccessful = 0
	statusTryLater   = 3
)

// parseBasicResponse decodes the outer OCSP envelope and the embedded
// BasicOCSPResponse, retaining the raw ResponseData bytes needed for
// signature verification.
func parseBasicResponse(raw []byte) (*basicOCSPResponse, error) {
	var envelope ocspResponse
	if rest, err := asn1.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("ocspcheck: cannot parse OCSP response envelope: %w", err)
	} else if len(rest) > 0 {
		return nil, errors.New("ocspcheck: trailing data after OCSP response envelope")
	}

	if envelope.Status != statusSuccessful {
		return nil, fmt.Errorf("ocspcheck: OCSP responder returned non-successful status %d", envelope.Status)
	}
	if len(envelope.ResponseBytes) == 0 {
		return nil, errors.New("ocspcheck: successful OCSP response carries no ResponseBytes")
	}
	rb := envelope.ResponseBytes[0]
	if !rb.ResponseType.Equal(oidBasicResponse) {
		return nil, fmt.Errorf("ocspcheck: unsupported OCSP response type %s", rb.ResponseType)
	}

	var basicDER []byte
	if _, err := asn1.Unmarshal(rb.Response.FullBytes, &basicDER); err != nil {
		return nil, fmt.Errorf("ocspcheck: cannot extract BasicOCSPResponse: %w", err)
	}

	var parsed struct {
		TBSResponseData    asn1.RawValue
		SignatureAlgorithm pkix.AlgorithmIdentifier
		Signature          asn1.BitString
		Certs              [][]byte `asn1:"explicit,tag:0,optional"`
	}
	if rest, err := asn1.Unmarshal(basicDER, &parsed); err != nil {
		return nil, fmt.Errorf("ocspcheck: cannot parse BasicOCSPResponse: %w", err)
	} else if len(rest) > 0 {
		return nil, errors.New("ocspcheck: trailing data after BasicOCSPResponse")
	}

	out := &basicOCSPResponse{
		signature: parsed.Signature,
	}
	out.tbs.RawTBSCertificate = parsed.TBSResponseData
	out.tbs.SignatureAlgorithm = parsed.SignatureAlgorithm

	var tbs struct {
		Version            int `asn1:"optional,default:0,explicit,tag:0"`
		ResponderID        asn1.RawValue
		ProducedAt         time.Time
		Responses          []singleResponse
		ResponseExtensions []pkix.Extension `asn1:"explicit,tag:1,optional"`
	}
	if _, err := asn1.Unmarshal(parsed.TBSResponseData.FullBytes, &tbs); err != nil {
		return nil, fmt.Errorf("ocspcheck: cannot parse ResponseData: %w", err)
	}
	out.tbs.Version = tbs.Version
	out.tbs.ResponderID = tbs.ResponderID
	out.tbs.ProducedAt = tbs.ProducedAt
	out.tbs.Responses = tbs.Responses
	out.tbs.ResponseExtensions = tbs.ResponseExtensions

	for _, certDER := range parsed.Certs {
		cert, err := x509.ParseCertificate(certDER)
		if err != nil {
			return nil, fmt.Errorf("ocspcheck: cannot parse embedded responder certificate: %w", err)
		}
		out.certificates = append(out.certificates, cert)
	}
	return out, nil
}
