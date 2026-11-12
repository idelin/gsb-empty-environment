package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"fmt"
)

// SignOptions configures construction of an exported authenticator.
type SignOptions struct {
	// Certificate is the leaf certificate proving the identity.
	Certificate *x509.Certificate
	// Chain contains intermediate certificates after Certificate, if any.
	Chain []*x509.Certificate
	// PrivateKey is the leaf certificate's private key.
	PrivateKey crypto.Signer
	// Scheme selects the TLS CertificateVerify signature algorithm.
	Scheme SignatureScheme
}

// BuildAuthenticator creates Certificate || CertificateVerify || Finished for
// the pending request using connection-bound key material from exporter.
func BuildAuthenticator(request Request, exporter Exporter, options SignOptions) ([]byte, error) {
	if exporter == nil {
		return nil, fmt.Errorf("%w: nil exporter", ErrExporter)
	}
	if request.ledger == nil {
		return nil, ErrRequestNotFound
	}
	if err := request.pending(request.ledger.now()); err != nil {
		return nil, err
	}
	if options.Certificate == nil || options.PrivateKey == nil {
		return nil, fmt.Errorf("%w: certificate and private key are required", ErrCertificate)
	}
	handshakeLabel, finishedLabel, err := request.role.ExporterLabels()
	if err != nil {
		return nil, err
	}
	hash := request.ledgerHash()
	if !hash.Available() {
		return nil, fmt.Errorf("%w: hash %v unavailable", ErrExporter, hash)
	}

	handshakeContext, err := exportKeyMaterial(exporter, handshakeLabel, hash.Size())
	if err != nil {
		return nil, err
	}
	finishedKey, err := exportKeyMaterial(exporter, finishedLabel, hash.Size())
	if err != nil {
		return nil, err
	}

	certificateMessage, err := encodeCertificateMessage(request.context, options.Certificate, options.Chain)
	if err != nil {
		return nil, err
	}
	verifyInput, err := CertificateVerifyInput(handshakeContext, request.wire, certificateMessage, hash)
	if err != nil {
		return nil, err
	}
	signature, err := signCertificateVerify(options.PrivateKey, options.Scheme, verifyInput)
	if err != nil {
		return nil, err
	}
	certificateVerifyMessage, err := encodeCertificateVerifyMessage(options.Scheme, signature)
	if err != nil {
		return nil, err
	}

	finishedTranscript, err := FinishedTranscript(
		handshakeContext,
		request.wire,
		certificateMessage,
		certificateVerifyMessage,
		hash,
	)
	if err != nil {
		return nil, err
	}
	finishedVerifyData := hmacBytes(finishedKey, finishedTranscript, hash)
	finishedMessage, err := marshalHandshake(finishedMessageType, finishedVerifyData)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, len(certificateMessage)+len(certificateVerifyMessage)+len(finishedMessage))
	out = append(out, certificateMessage...)
	out = append(out, certificateVerifyMessage...)
	out = append(out, finishedMessage...)
	return out, nil
}

func encodeCertificateMessage(context []byte, leaf *x509.Certificate, chain []*x509.Certificate) ([]byte, error) {
	if len(context) > 255 {
		return nil, fmt.Errorf("%w: request context too long", ErrMalformed)
	}
	certs := make([]*x509.Certificate, 0, 1+len(chain))
	certs = append(certs, leaf)
	certs = append(certs, chain...)

	certList := make([]byte, 0, 1024)
	for _, cert := range certs {
		if cert == nil {
			return nil, fmt.Errorf("%w: nil certificate in chain", ErrCertificate)
		}
		if len(cert.Raw) == 0 || len(cert.Raw) > 0xffff {
			return nil, fmt.Errorf("%w: invalid certificate size", ErrCertificate)
		}
		certList = binary.BigEndian.AppendUint16(certList, uint16(len(cert.Raw)))
		certList = append(certList, cert.Raw...)
		certList = append(certList, 0, 0) // empty CertificateEntry extensions
	}
	if len(certList) > 0xffff {
		return nil, fmt.Errorf("%w: certificate_list too long", ErrMalformed)
	}

	body := make([]byte, 0, 1+len(context)+2+len(certList))
	body = append(body, byte(len(context)))
	body = append(body, context...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(certList)))
	body = append(body, certList...)
	return marshalHandshake(certificateMessageType, body)
}

func encodeCertificateVerifyMessage(scheme SignatureScheme, signature []byte) ([]byte, error) {
	if len(signature) == 0 || len(signature) > 0xffff {
		return nil, fmt.Errorf("%w: invalid signature size", ErrMalformed)
	}
	body := make([]byte, 0, 4+len(signature))
	body = binary.BigEndian.AppendUint16(body, uint16(scheme))
	body = binary.BigEndian.AppendUint16(body, uint16(len(signature)))
	body = append(body, signature...)
	return marshalHandshake(certificateVerifyMessageType, body)
}

func marshalHandshake(messageType byte, body []byte) ([]byte, error) {
	if len(body) > 0xffffff {
		return nil, fmt.Errorf("%w: handshake message too long", ErrMalformed)
	}
	out := make([]byte, 4+len(body))
	out[0] = messageType
	out[1] = byte(len(body) >> 16)
	out[2] = byte(len(body) >> 8)
	out[3] = byte(len(body))
	copy(out[4:], body)
	return out, nil
}

func signCertificateVerify(privateKey crypto.Signer, scheme SignatureScheme, input []byte) ([]byte, error) {
	if scheme == SignatureEd25519 {
		return privateKey.Sign(rand.Reader, input, crypto.Hash(0))
	}

	hash, err := scheme.hash()
	if err != nil {
		return nil, err
	}
	digest := hashBytes(hash, input)
	if scheme.isPSS() {
		return privateKey.Sign(rand.Reader, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hash,
		})
	}
	return privateKey.Sign(rand.Reader, digest, hash)
}

func hmacBytes(key, message []byte, hash crypto.Hash) []byte {
	mac := hmac.New(hash.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func exportKeyMaterial(exporter Exporter, label string, length int) ([]byte, error) {
	material, err := exporter.ExportKeyingMaterial(label, nil, length)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExporter, err)
	}
	if len(material) != length {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrExporter, len(material), length)
	}
	return append([]byte(nil), material...), nil
}

func (r Request) ledgerHash() crypto.Hash {
	if r.ledger == nil {
		return 0
	}
	return r.ledger.hash
}
