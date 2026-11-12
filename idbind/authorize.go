package idbind

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// AuthRequest carries everything the responder's SA authorization step
// needs to authenticate and authorize an IKE_AUTH exchange.
type AuthRequest struct {
	// RealMessage1 is the initiator's IKE_SA_INIT request exactly as
	// received on the wire, including the IKE header.
	RealMessage1 []byte
	// NonceR is the responder's nonce payload data (no payload header).
	NonceR []byte
	// IDiPayload is the full IDi payload including the generic header.
	IDiPayload []byte
	// IDrPayload is the full IDr payload including the generic header.
	IDrPayload []byte
	// SKPi is SK_pi from the IKE SA keymat.
	SKPi []byte
	// PRF is the negotiated pseudo-random function.
	PRF PRF
	// AuthMethod and AuthSignature are from the initiator's AUTH payload.
	AuthMethod    AuthMethod
	AuthSignature []byte
	// PeerCert is the initiator's certificate, already path-validated by
	// the certificate verification step.
	PeerCert *x509.Certificate
	// OwnID is the responder's own identity; the received IDr must match.
	OwnID *IDPayload
	// TSi and TSr are the traffic selectors proposed for the CHILD_SA.
	TSi []TrafficSelector
	TSr []TrafficSelector
}

// Authorize authenticates the initiator and authorizes the CHILD_SA
// proposal. On success it returns the certificate-bound initiator
// identity and its policy. The checks run in a fail-closed order:
// signature over the reconstructed signed octets, IDr sanity, IDi-to-
// certificate binding, then traffic selector confinement.
func Authorize(req *AuthRequest, store *PolicyStore) (*IDPayload, *Policy, error) {
	if req.PRF == nil {
		return nil, nil, errors.New("idbind: PRF is required")
	}
	if req.PeerCert == nil {
		return nil, nil, errors.New("idbind: peer certificate is required")
	}
	idi, err := ParseIDPayload(req.IDiPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("idbind: parsing IDi: %w", err)
	}
	idr, err := ParseIDPayload(req.IDrPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("idbind: parsing IDr: %w", err)
	}

	// 1. Rebuild the signed octets (RFC 7296 Section 2.15) and verify
	//    the AUTH signature. This binds IDi, the first message, and the
	//    responder nonce to the peer's private key.
	signed := BuildInitiatorSignedOctets(req.RealMessage1, req.NonceR, idi.Body(), req.SKPi, req.PRF)
	if err := VerifyAuth(req.PeerCert, signed, req.AuthSignature, req.AuthMethod); err != nil {
		return nil, nil, err
	}

	// 2. The IDr the initiator sent must name this responder.
	if err := VerifyResponderID(idr, req.OwnID); err != nil {
		return nil, nil, err
	}

	// 3. The claimed IDi must be bound to the certificate; a valid
	//    certificate must not be usable to claim another identity.
	if err := BindIdentity(idi, req.PeerCert); err != nil {
		return nil, nil, err
	}

	// 4. Fetch the policy for the now-authenticated identity and confine
	//    the CHILD_SA proposal to it: narrowing is allowed, exceeding is
	//    not.
	policy, err := store.PolicyFor(idi)
	if err != nil {
		return nil, nil, err
	}
	if err := policy.AuthorizeProposal(req.TSi); err != nil {
		return nil, nil, fmt.Errorf("idbind: TSi: %w", err)
	}
	if err := policy.AuthorizeProposal(req.TSr); err != nil {
		return nil, nil, fmt.Errorf("idbind: TSr: %w", err)
	}
	return idi, policy, nil
}
