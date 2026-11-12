package netsec

import (
	"crypto"
	"crypto/x509"
	"fmt"
)

// Exchange holds the material needed to check AUTH payloads in the second
// IKEv2 exchange (IKE_AUTH). It contains the initiator's original SA_INIT
// message and both nonces/identities, so callers cannot accidentally omit
// material covered by the signature.
type Exchange struct {
	// RealMessage1 is the exact initiator-to-responder IKE_SA_INIT message,
	// including its IKE header and all payloads.
	RealMessage1 []byte

	InitiatorNonce []byte
	ResponderNonce []byte

	// InitiatorID and ResponderID are complete IKEv2 ID payload bodies.
	InitiatorID []byte
	ResponderID []byte

	// InitiatorSKp and ResponderSKp are SK_pi and SK_pr from the IKE SA.
	InitiatorSKp []byte
	ResponderSKp []byte

	// Certificates are the identity certificates sent by each side. The
	// responder certificate is mandatory when verifying responder AUTH.
	InitiatorCertificate *x509.Certificate
	ResponderCertificate *x509.Certificate

	PRF PRF

	// SignatureHash optionally pins the signature hash; zero auto-selects.
	SignatureHash crypto.Hash
}

// ResponderParams builds the AUTH parameters for the responder's payload as
// seen by the initiator.
func (e Exchange) ResponderParams() (AuthParams, error) {
	if len(e.ResponderNonce) == 0 {
		return AuthParams{}, fmt.Errorf("%w: empty responder nonce", ErrInvalidAuthParams)
	}
	if len(e.ResponderID) == 0 {
		return AuthParams{}, fmt.Errorf("%w: empty responder identity", ErrInvalidAuthParams)
	}
	return AuthParams{
		RealMessage1:    e.RealMessage1,
		PeerNonce:       e.ResponderNonce,
		OwnNonce:        e.InitiatorNonce,
		PeerIdentity:    e.ResponderID,
		OwnIdentity:     e.InitiatorID,
		SKp:             e.ResponderSKp,
		PRF:             e.PRF,
		PeerCertificate: e.ResponderCertificate,
		PeerIsResponder: true,
		SignatureHash:   e.SignatureHash,
	}, nil
}

// InitiatorParams builds the AUTH parameters for the initiator's payload as
// seen by the responder.
func (e Exchange) InitiatorParams() (AuthParams, error) {
	if len(e.InitiatorNonce) == 0 {
		return AuthParams{}, fmt.Errorf("%w: empty initiator nonce", ErrInvalidAuthParams)
	}
	if len(e.InitiatorID) == 0 {
		return AuthParams{}, fmt.Errorf("%w: empty initiator identity", ErrInvalidAuthParams)
	}
	return AuthParams{
		RealMessage1:    e.RealMessage1,
		PeerNonce:       e.InitiatorNonce,
		OwnNonce:        e.ResponderNonce,
		PeerIdentity:    e.InitiatorID,
		OwnIdentity:     e.ResponderID,
		SKp:             e.InitiatorSKp,
		PRF:             e.PRF,
		PeerCertificate: e.InitiatorCertificate,
		PeerIsResponder: false,
		SignatureHash:   e.SignatureHash,
	}, nil
}

// ResponderAuthInput returns the data signed by the responder's AUTH payload.
func (e Exchange) ResponderAuthInput() ([]byte, error) {
	p, err := e.ResponderParams()
	if err != nil {
		return nil, err
	}
	return p.AuthInput()
}

// InitiatorAuthInput returns the data signed by the initiator's AUTH payload
// when checked by the responder.
func (e Exchange) InitiatorAuthInput() ([]byte, error) {
	p, err := e.InitiatorParams()
	if err != nil {
		return nil, err
	}
	return p.AuthInput()
}

// VerifyResponderAuth checks the responder's AUTH payload and requires its
// ID payload to match a SAN in ResponderCertificate.
func (e Exchange) VerifyResponderAuth(method AuthMethod, authKey, auth []byte) error {
	p, err := e.ResponderParams()
	if err != nil {
		return err
	}
	return VerifyResponderAuth(p, method, authKey, auth)
}

// VerifyInitiatorAuth checks the initiator's AUTH payload. If
// InitiatorCertificate is present, it is used as the signature verification
// key; a responder-issued AUTH must use VerifyResponderAuth instead.
func (e Exchange) VerifyInitiatorAuth(method AuthMethod, authKey, auth []byte) error {
	p, err := e.InitiatorParams()
	if err != nil {
		return err
	}
	return VerifyInitiatorAuth(p, method, authKey, auth)
}
