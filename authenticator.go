package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	certificateMessageType        byte = 11
	certificateRequestMessageType byte = 13
	certificateVerifyMessageType  byte = 15
	finishedMessageType           byte = 20
	clientCertificateRequestType  byte = 17

	exporterClientHandshakeContext = "EXPORTER-client authenticator handshake context"
	exporterServerHandshakeContext = "EXPORTER-server authenticator handshake context"
	exporterClientFinishedKey      = "EXPORTER-client authenticator finished key"
	exporterServerFinishedKey      = "EXPORTER-server authenticator finished key"

	certificateVerifyContextString = "Exported Authenticator"
)

var (
	// ErrRequestNotFound means that the verifier has no outstanding request
	// matching the supplied request object.
	ErrRequestNotFound = errors.New("netsec: authenticator request is not pending")
	// ErrRequestUsed means that an authenticator request was already consumed.
	ErrRequestUsed = errors.New("netsec: authenticator request was already used")
	// ErrRequestExpired means that an authenticator request exceeded its lifetime.
	ErrRequestExpired = errors.New("netsec: authenticator request expired")
	// ErrMalformed means that an authenticator or request was not valid TLS
	// handshake message encoding.
	ErrMalformed = errors.New("netsec: malformed exported authenticator")
	// ErrExporter means that the connection exporter failed or returned an
	// unexpected amount of key material.
	ErrExporter = errors.New("netsec: key exporter failure")
	// ErrCertificate means that the certificate chain could not be validated.
	ErrCertificate = errors.New("netsec: certificate validation failed")
	// ErrRequestContextMismatch means the Certificate message did not echo the
	// certificate_request_context of the pending request.
	ErrRequestContextMismatch = errors.New("netsec: request context mismatch")
	// ErrSignature means the CertificateVerify signature was invalid.
	ErrSignature = errors.New("netsec: certificate verify signature invalid")
	// ErrFinished means the Finished MAC was invalid.
	ErrFinished = errors.New("netsec: finished MAC invalid")
	// ErrUnsupportedSignatureScheme means the certificate key or signature
	// scheme is not supported by this package.
	ErrUnsupportedSignatureScheme = errors.New("netsec: unsupported signature scheme")
)

// Role identifies which endpoint created the exported authenticator.
type Role uint8

const (
	// RoleServer means the authenticator was created by the TLS server.
	RoleServer Role = iota
	// RoleClient means the authenticator was created by the TLS client.
	RoleClient
)

// ExporterLabels returns the RFC 9261 exporter labels for the endpoint that
// created the authenticator.
func (r Role) ExporterLabels() (handshakeContext, finishedKey string, err error) {
	switch r {
	case RoleServer:
		return exporterServerHandshakeContext, exporterServerFinishedKey, nil
	case RoleClient:
		return exporterClientHandshakeContext, exporterClientFinishedKey, nil
	default:
		return "", "", fmt.Errorf("%w: unknown role %d", ErrMalformed, r)
	}
}

func (r Role) requestMessageType() (byte, error) {
	switch r {
	case RoleServer:
		// A server authenticator answers a client-generated request.
		return clientCertificateRequestType, nil
	case RoleClient:
		// A client authenticator answers a server-generated request.
		return certificateRequestMessageType, nil
	default:
		return 0, fmt.Errorf("%w: unknown role %d", ErrMalformed, r)
	}
}

// SignatureScheme is a TLS SignatureScheme value.
type SignatureScheme uint16

const (
	SignatureECDSASecp256r1SHA256 SignatureScheme = 0x0403
	SignatureECDSASecp384r1SHA384 SignatureScheme = 0x0503
	SignatureECDSASecp521r1SHA512 SignatureScheme = 0x0603

	SignatureRSAPKCS1SHA256 SignatureScheme = 0x0401
	SignatureRSAPKCS1SHA384 SignatureScheme = 0x0501
	SignatureRSAPKCS1SHA512 SignatureScheme = 0x0601

	SignatureRSAPSSRSAESHA256 SignatureScheme = 0x0804
	SignatureRSAPSSRSAESHA384 SignatureScheme = 0x0805
	SignatureRSAPSSRSAESHA512 SignatureScheme = 0x0806
	SignatureEd25519          SignatureScheme = 0x0807
	SignatureEd448            SignatureScheme = 0x0808
	SignatureRSAPSSPSSSHA256  SignatureScheme = 0x0809
	SignatureRSAPSSPSSSHA384  SignatureScheme = 0x080a
	SignatureRSAPSSPSSSHA512  SignatureScheme = 0x080b
)

func (s SignatureScheme) hash() (crypto.Hash, error) {
	switch s {
	case SignatureECDSASecp256r1SHA256, SignatureRSAPKCS1SHA256,
		SignatureRSAPSSRSAESHA256, SignatureRSAPSSPSSSHA256:
		return crypto.SHA256, nil
	case SignatureECDSASecp384r1SHA384, SignatureRSAPKCS1SHA384,
		SignatureRSAPSSRSAESHA384, SignatureRSAPSSPSSSHA384:
		return crypto.SHA384, nil
	case SignatureECDSASecp521r1SHA512, SignatureRSAPKCS1SHA512,
		SignatureRSAPSSRSAESHA512, SignatureRSAPSSPSSSHA512:
		return crypto.SHA512, nil
	case SignatureEd25519:
		return 0, nil
	default:
		return 0, fmt.Errorf("%w: %#04x", ErrUnsupportedSignatureScheme, uint16(s))
	}
}

func (s SignatureScheme) isRSA() bool {
	switch s {
	case SignatureRSAPKCS1SHA256, SignatureRSAPKCS1SHA384, SignatureRSAPKCS1SHA512,
		SignatureRSAPSSRSAESHA256, SignatureRSAPSSRSAESHA384, SignatureRSAPSSRSAESHA512,
		SignatureRSAPSSPSSSHA256, SignatureRSAPSSPSSSHA384, SignatureRSAPSSPSSSHA512:
		return true
	default:
		return false
	}
}

func (s SignatureScheme) isPSS() bool {
	switch s {
	case SignatureRSAPSSRSAESHA256, SignatureRSAPSSRSAESHA384, SignatureRSAPSSRSAESHA512,
		SignatureRSAPSSPSSSHA256, SignatureRSAPSSPSSSHA384, SignatureRSAPSSPSSSHA512:
		return true
	default:
		return false
	}
}

// Authenticator is the parsed form of RFC 9261's
// Certificate || CertificateVerify || Finished sequence.
type Authenticator struct {
	Certificate               []byte
	CertificateVerify         []byte
	Finished                  []byte
	CertificateRequestContext []byte
	CertificateChainDER       [][]byte
	SignatureScheme           SignatureScheme
	Signature                 []byte
	FinishedVerifyData        []byte
}

// ParseAuthenticator parses three consecutive TLS handshake messages. The
// returned byte slices preserve the exact wire encoding used by the transcript
// hashes.
func ParseAuthenticator(raw []byte) (*Authenticator, error) {
	certStart, certEnd, certBody, err := readHandshakeMessage(raw, 0, certificateMessageType)
	if err != nil {
		return nil, err
	}
	cvStart, cvEnd, cvBody, err := readHandshakeMessage(raw, certEnd, certificateVerifyMessageType)
	if err != nil {
		return nil, err
	}
	finStart, finEnd, finBody, err := readHandshakeMessage(raw, cvEnd, finishedMessageType)
	if err != nil {
		return nil, err
	}
	if finEnd != len(raw) {
		return nil, fmt.Errorf("%w: trailing %d bytes", ErrMalformed, len(raw)-finEnd)
	}

	requestContext, certs, err := parseCertificateBody(certBody)
	if err != nil {
		return nil, err
	}
	scheme, signature, err := parseCertificateVerifyBody(cvBody)
	if err != nil {
		return nil, err
	}
	if len(finBody) == 0 {
		return nil, fmt.Errorf("%w: empty Finished verify_data", ErrMalformed)
	}

	return &Authenticator{
		Certificate:               raw[certStart:certEnd],
		CertificateVerify:         raw[cvStart:cvEnd],
		Finished:                  raw[finStart:finEnd],
		CertificateRequestContext: requestContext,
		CertificateChainDER:       certs,
		SignatureScheme:           scheme,
		Signature:                 signature,
		FinishedVerifyData:        append([]byte(nil), finBody...),
	}, nil
}

func readHandshakeMessage(raw []byte, offset int, wantType byte) (start, end int, body []byte, err error) {
	if offset < 0 || len(raw)-offset < 4 {
		return 0, 0, nil, fmt.Errorf("%w: truncated handshake header", ErrMalformed)
	}
	if raw[offset] != wantType {
		return 0, 0, nil, fmt.Errorf("%w: unexpected handshake type %d", ErrMalformed, raw[offset])
	}
	length := int(raw[offset+1])<<16 | int(raw[offset+2])<<8 | int(raw[offset+3])
	if length > len(raw)-offset-4 {
		return 0, 0, nil, fmt.Errorf("%w: truncated handshake message", ErrMalformed)
	}
	start = offset
	end = offset + 4 + length
	return start, end, raw[offset+4 : end], nil
}

func parseCertificateBody(body []byte) ([]byte, [][]byte, error) {
	if len(body) < 1 {
		return nil, nil, fmt.Errorf("%w: truncated Certificate", ErrMalformed)
	}
	contextLen := int(body[0])
	pos := 1
	if contextLen > len(body)-pos {
		return nil, nil, fmt.Errorf("%w: truncated certificate_request_context", ErrMalformed)
	}
	context := append([]byte(nil), body[pos:pos+contextLen]...)
	pos += contextLen

	if len(body)-pos < 2 {
		return nil, nil, fmt.Errorf("%w: truncated certificate_list", ErrMalformed)
	}
	listLen := int(body[pos])<<8 | int(body[pos+1])
	pos += 2
	if listLen != len(body)-pos {
		return nil, nil, fmt.Errorf("%w: invalid certificate_list length", ErrMalformed)
	}

	var certs [][]byte
	limit := pos + listLen
	for pos < limit {
		if limit-pos < 2 {
			return nil, nil, fmt.Errorf("%w: truncated certificate entry", ErrMalformed)
		}
		certLen := int(body[pos])<<8 | int(body[pos+1])
		pos += 2
		if certLen == 0 || certLen > limit-pos {
			return nil, nil, fmt.Errorf("%w: invalid certificate length", ErrMalformed)
		}
		certs = append(certs, append([]byte(nil), body[pos:pos+certLen]...))
		pos += certLen
		if limit-pos < 2 {
			return nil, nil, fmt.Errorf("%w: truncated certificate extensions", ErrMalformed)
		}
		extLen := int(body[pos])<<8 | int(body[pos+1])
		pos += 2
		if extLen > limit-pos {
			return nil, nil, fmt.Errorf("%w: invalid certificate extensions length", ErrMalformed)
		}
		pos += extLen
	}
	if len(certs) == 0 {
		return nil, nil, fmt.Errorf("%w: empty certificate_list", ErrMalformed)
	}
	return context, certs, nil
}

func parseCertificateVerifyBody(body []byte) (SignatureScheme, []byte, error) {
	if len(body) < 4 {
		return 0, nil, fmt.Errorf("%w: truncated CertificateVerify", ErrMalformed)
	}
	scheme := SignatureScheme(binary.BigEndian.Uint16(body[:2]))
	sigLen := int(binary.BigEndian.Uint16(body[2:4]))
	if sigLen == 0 || sigLen != len(body)-4 {
		return 0, nil, fmt.Errorf("%w: invalid signature length", ErrMalformed)
	}
	return scheme, append([]byte(nil), body[4:]...), nil
}

// CertificateVerifyInput returns the exact bytes passed to the signature
// scheme. The final component is Hash(Handshake Context || authenticator request
// || Certificate message).
func CertificateVerifyInput(handshakeContext, authenticatorRequest, certificateMessage []byte, hash crypto.Hash) ([]byte, error) {
	if !hash.Available() {
		return nil, fmt.Errorf("%w: hash %v unavailable", ErrExporter, hash)
	}
	transcript := hashBytes(hash, handshakeContext, authenticatorRequest, certificateMessage)
	input := make([]byte, 64, 64+len(certificateVerifyContextString)+1+len(transcript))
	for i := range input {
		input[i] = 0x20
	}
	input = append(input, certificateVerifyContextString...)
	input = append(input, 0)
	input = append(input, transcript...)
	return input, nil
}

// FinishedTranscript returns Hash(Handshake Context || authenticator request ||
// Certificate || CertificateVerify).
func FinishedTranscript(handshakeContext, authenticatorRequest, certificateMessage, certificateVerifyMessage []byte, hash crypto.Hash) ([]byte, error) {
	if !hash.Available() {
		return nil, fmt.Errorf("%w: hash %v unavailable", ErrExporter, hash)
	}
	return hashBytes(hash, handshakeContext, authenticatorRequest, certificateMessage, certificateVerifyMessage), nil
}

func hashBytes(h crypto.Hash, parts ...[]byte) []byte {
	digest := h.New()
	for _, part := range parts {
		_, _ = digest.Write(part)
	}
	return digest.Sum(nil)
}

func verifyCertificateSignature(leaf *x509.Certificate, scheme SignatureScheme, signature, input []byte) error {
	switch scheme {
	case SignatureEd25519:
		publicKey, ok := leaf.PublicKey.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(publicKey, input, signature) {
			return ErrSignature
		}
		return nil
	case SignatureEd448:
		return fmt.Errorf("%w: Ed448", ErrUnsupportedSignatureScheme)
	}

	hash, err := scheme.hash()
	if err != nil {
		return err
	}
	if !hash.Available() {
		return fmt.Errorf("%w: hash %v unavailable", ErrUnsupportedSignatureScheme, hash)
	}
	digest := hashBytes(hash, input)

	switch {
	case scheme == SignatureECDSASecp256r1SHA256 ||
		scheme == SignatureECDSASecp384r1SHA384 ||
		scheme == SignatureECDSASecp521r1SHA512:
		publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not ECDSA", ErrUnsupportedSignatureScheme)
		}
		if want := expectedECDSABits(scheme); publicKey.Curve.Params().BitSize != want {
			return fmt.Errorf("%w: ECDSA curve does not match scheme", ErrUnsupportedSignatureScheme)
		}
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			return ErrSignature
		}
		return nil
	case scheme.isRSA():
		publicKey, ok := leaf.PublicKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not RSA", ErrUnsupportedSignatureScheme)
		}
		if scheme.isPSS() {
			if err := rsa.VerifyPSS(publicKey, hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthEqualsHash,
				Hash:       hash,
			}); err != nil {
				return ErrSignature
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(publicKey, hash, digest, signature); err != nil {
			return ErrSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: %#04x", ErrUnsupportedSignatureScheme, uint16(scheme))
	}
}

func expectedECDSABits(scheme SignatureScheme) int {
	switch scheme {
	case SignatureECDSASecp256r1SHA256:
		return 256
	case SignatureECDSASecp384r1SHA384:
		return 384
	case SignatureECDSASecp521r1SHA512:
		return 521
	default:
		return 0
	}
}

func verifyFinished(key, expected, actual []byte, hash crypto.Hash) error {
	mac := hmac.New(hash.New, key)
	_, _ = mac.Write(expected)
	want := mac.Sum(nil)
	if len(actual) != len(want) || subtle.ConstantTimeCompare(want, actual) != 1 {
		return ErrFinished
	}
	return nil
}
