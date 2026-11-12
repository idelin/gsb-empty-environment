package idbind

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
)

// AuthMethod identifies the IKEv2 AUTH payload method (RFC 7296
// Section 3.8 and RFC 7427).
type AuthMethod uint8

const (
	// AuthRSASignature is RSA Digital Signature (method 1): RSASSA-PKCS1-v1_5
	// with SHA-1 over the signed octets per RFC 7296.
	AuthRSASignature AuthMethod = 1
	// AuthECDSASha256 is ECDSA with SHA-256 on the P-256 curve (method 9,
	// RFC 4754), carrying an ASN.1 DER encoded ECDSA signature.
	AuthECDSASha256 AuthMethod = 9
	// AuthDigitalSignature is the generic Digital Signature method (14,
	// RFC 7427); this implementation verifies RSASSA-PKCS1-v1_5 with
	// SHA-256.
	AuthDigitalSignature AuthMethod = 14
)

// VerifyAuth verifies the initiator's AUTH payload signature over the
// signed octets reconstructed by BuildInitiatorSignedOctets, using the
// public key of the (already path-validated) peer certificate.
func VerifyAuth(cert *x509.Certificate, signedOctets, signature []byte, method AuthMethod) error {
	switch method {
	case AuthECDSASha256:
		pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("idbind: AUTH method %d requires ECDSA key, certificate has %T", method, cert.PublicKey)
		}
		digest := sha256.Sum256(signedOctets)
		if !ecdsa.VerifyASN1(pub, digest[:], signature) {
			return errors.New("idbind: AUTH signature verification failed")
		}
		return nil
	case AuthRSASignature, AuthDigitalSignature:
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("idbind: AUTH method %d requires RSA key, certificate has %T", method, cert.PublicKey)
		}
		digest := sha256.Sum256(signedOctets)
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
			return errors.New("idbind: AUTH signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("idbind: unsupported AUTH method %d", method)
	}
}

// BindIdentity verifies that the claimed IDi payload is bound to the
// peer certificate: the ID type and value must match a subjectAltName of
// the corresponding kind, or — when the certificate carries no SAN of
// that kind — the subject. A certificate that is valid for one identity
// must not be usable to claim another.
func BindIdentity(id *IDPayload, cert *x509.Certificate) error {
	switch id.Type {
	case ID_IPV4_ADDR, ID_IPV6_ADDR:
		ip := net.IP(id.Data)
		if id.Type == ID_IPV4_ADDR && len(id.Data) != net.IPv4len {
			return fmt.Errorf("idbind: %s with %d-byte value", id.Type, len(id.Data))
		}
		if id.Type == ID_IPV6_ADDR && len(id.Data) != net.IPv6len {
			return fmt.Errorf("idbind: %s with %d-byte value", id.Type, len(id.Data))
		}
		for _, san := range cert.IPAddresses {
			if san.Equal(ip) {
				return nil
			}
		}
		return fmt.Errorf("idbind: %s %v not present in certificate IP SANs", id.Type, ip)

	case ID_FQDN:
		name := string(id.Data)
		for _, san := range cert.DNSNames {
			if strings.EqualFold(san, name) {
				return nil
			}
		}
		if len(cert.DNSNames) == 0 && strings.EqualFold(cert.Subject.CommonName, name) {
			return nil
		}
		return fmt.Errorf("idbind: ID_FQDN %q not bound to certificate", name)

	case ID_RFC822_ADDR:
		mailbox := string(id.Data)
		for _, san := range cert.EmailAddresses {
			if strings.EqualFold(san, mailbox) {
				return nil
			}
		}
		if len(cert.EmailAddresses) == 0 && strings.EqualFold(cert.Subject.CommonName, mailbox) {
			return nil
		}
		return fmt.Errorf("idbind: ID_RFC822_ADDR %q not bound to certificate", mailbox)

	case ID_DER_ASN1_DN:
		if bytes.Equal(id.Data, cert.RawSubject) {
			return nil
		}
		return errors.New("idbind: ID_DER_ASN1_DN does not equal certificate subject")

	default:
		return fmt.Errorf("idbind: ID type %s cannot be bound to a certificate", id.Type)
	}
}

// VerifyResponderID checks that the IDr payload received in IKE_AUTH
// names this responder. A tampered or swapped IDr must abort the
// exchange before any authorization decision.
func VerifyResponderID(idr *IDPayload, ownID *IDPayload) error {
	if idr.Type != ownID.Type {
		return fmt.Errorf("idbind: IDr type %s does not match responder identity type %s", idr.Type, ownID.Type)
	}
	if !bytes.Equal(normalizeIDValue(idr), normalizeIDValue(ownID)) {
		return fmt.Errorf("idbind: IDr value does not match responder identity")
	}
	return nil
}

func normalizeIDValue(id *IDPayload) []byte {
	switch id.Type {
	case ID_FQDN, ID_RFC822_ADDR:
		return []byte(strings.ToLower(string(id.Data)))
	default:
		return id.Data
	}
}
