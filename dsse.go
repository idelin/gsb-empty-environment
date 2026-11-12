package netsec

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// InTotoPayloadType is the only DSSE payload type accepted by this
	// package.  The value is part of the signed pre-authentication encoding,
	// so it must not be relaxed or normalised.
	InTotoPayloadType = "application/vnd.in-toto+json"
)

var (
	ErrUnknownPayloadType = errors.New("netsec: unknown DSSE payload type")
	ErrDuplicateSigner    = errors.New("netsec: duplicate DSSE signer")
	ErrEmptySignatures    = errors.New("netsec: DSSE envelope has no signatures")
)

// Envelope is the JSON representation of a DSSE envelope.
//
// Certificate fields are optional extensions used by admission hosts that
// receive the signing certificate in the envelope rather than out of band.
// When present, the signature itself is still verified over Envelope.PAE.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"`
	Signatures  []Signature `json:"signatures"`

	Certificates     []string `json:"certificates,omitempty"`
	CertificateChain []string `json:"certificateChain,omitempty"`
}

// Signature is a DSSE signature entry.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`

	Certificate             string   `json:"certificate,omitempty"`
	Cert                    string   `json:"cert,omitempty"`
	X509Certificate         string   `json:"x509Certificate,omitempty"`
	CertificateChain        []string `json:"certificateChain,omitempty"`
	X509CertificateChain    []string `json:"x509CertificateChain,omitempty"`
	CertificateChainPEM     []string `json:"certificateChainPEM,omitempty"`
	X509CertificateChainPEM []string `json:"x509CertificateChainPEM,omitempty"`
}

// PAE constructs the exact DSSE pre-authentication encoding from the DSSE
// specification:
//
//	"DSSEv1" SP <len(payloadType)> SP <payloadType> SP <len(payload)> SP <payload>
//
// Lengths are decimal byte counts.  In particular, no JSON encoding and no
// intermediate string conversion of the payload is used.
func PAE(payloadType string, payload []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len("DSSEv1") + len(payloadType) + len(payload) + 8)
	buf.WriteString("DSSEv1")
	buf.WriteByte(' ')
	buf.WriteString(strconv.Itoa(len(payloadType)))
	buf.WriteByte(' ')
	buf.WriteString(payloadType)
	buf.WriteByte(' ')
	buf.WriteString(strconv.Itoa(len(payload)))
	buf.WriteByte(' ')
	buf.Write(payload)
	return buf.Bytes()
}

// PreAuthEncoding is an alias for PAE.
func PreAuthEncoding(payloadType string, payload []byte) []byte {
	return PAE(payloadType, payload)
}

func validateEnvelope(env Envelope) ([]byte, []byte, error) {
	if env.PayloadType != InTotoPayloadType {
		return nil, nil, fmt.Errorf("%w: %q", ErrUnknownPayloadType, env.PayloadType)
	}
	if len(env.Signatures) == 0 {
		return nil, nil, ErrEmptySignatures
	}

	seenKeyIDs := make(map[string]struct{}, len(env.Signatures))
	seenKeys := make(map[string]struct{}, len(env.Signatures))
	for i, signature := range env.Signatures {
		if signature.KeyID != "" {
			if _, ok := seenKeyIDs[signature.KeyID]; ok {
				return nil, nil, fmt.Errorf("%w: keyid %q appears more than once", ErrDuplicateSigner, signature.KeyID)
			}
			seenKeyIDs[signature.KeyID] = struct{}{}
		}

		certificateData := signature.certificateData()
		if certificateData != "" {
			certs, err := decodeCertificates(certificateData)
			if err != nil {
				return nil, nil, fmt.Errorf("signature %d certificate: %w", i, err)
			}
			if len(certs) > 0 {
				fingerprint, err := publicKeyFingerprint(certs[0])
				if err != nil {
					return nil, nil, fmt.Errorf("signature %d certificate key: %w", i, err)
				}
				if _, ok := seenKeys[fingerprint]; ok {
					return nil, nil, fmt.Errorf("%w: certificate key appears more than once", ErrDuplicateSigner)
				}
				seenKeys[fingerprint] = struct{}{}
			}
		}
	}

	payload, err := decodeBase64(env.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("DSSE payload: %w", err)
	}
	if len(payload) == 0 {
		return nil, nil, errors.New("netsec: DSSE payload is empty")
	}
	return payload, PAE(env.PayloadType, payload), nil
}

func (s Signature) certificateData() string {
	for _, value := range []string{s.Certificate, s.Cert, s.X509Certificate} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s Signature) certificateChainData() []string {
	chain := make([]string, 0, len(s.CertificateChain)+len(s.X509CertificateChain)+len(s.CertificateChainPEM)+len(s.X509CertificateChainPEM))
	for _, values := range [][]string{s.CertificateChain, s.X509CertificateChain, s.CertificateChainPEM, s.X509CertificateChainPEM} {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				chain = append(chain, value)
			}
		}
	}
	return chain
}

func decodeBase64(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("empty base64 value")
	}
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '\t', '\n', '\r':
			return nil, errors.New("base64 value contains whitespace")
		}
	}

	encodings := []*base64.Encoding{
		base64.StdEncoding.Strict(),
		base64.RawStdEncoding.Strict(),
		base64.URLEncoding.Strict(),
		base64.RawURLEncoding.Strict(),
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("invalid base64 value: %w", lastErr)
}
