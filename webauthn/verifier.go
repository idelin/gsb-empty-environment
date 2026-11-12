package webauthn

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// ClientData is the subset of CollectedClientData that is relevant to origin
// policy. Assertion verifiers already parse it for the type and challenge
// fields; those checks stay outside this package.
type ClientData struct {
	// Origin is the serialized origin of the frame that invoked
	// navigator.credentials.get().
	Origin string `json:"origin"`
	// CrossOrigin is the optional clientData crossOrigin flag. It is true
	// whenever the credential call originates in a frame whose origin differs
	// from the top-level document's origin.
	CrossOrigin bool `json:"crossOrigin,omitempty"`
	// TopOrigin is the serialized origin of the top-level document, present
	// when the browser supports top-origin tracking (including same-origin
	// nested-frame scenarios).
	TopOrigin string `json:"topOrigin,omitempty"`
}

// AssertionRequest carries the assertion inputs that origin and RP ID policy
// depend on.
type AssertionRequest struct {
	// ClientData is the parsed CollectedClientData returned by the browser.
	ClientData ClientData
	// AuthenticatorData is the raw authenticator data structure from the
	// assertion. Its first 32 bytes must equal SHA-256(RPID).
	AuthenticatorData []byte
}

// Verifier enforces origin and RP ID policy against a fixed RP ID and its
// allow-listed manifest. It is safe for concurrent use once constructed: a
// Manifest is never mutated after parsing.
type Verifier struct {
	rpID     string
	rpIDHash [sha256.Size]byte
	manifest *Manifest
}

// NewVerifier builds a Verifier for rpID and the given manifest. The RP ID is
// validated and its expected rpIdHash is precomputed.
func NewVerifier(rpID string, manifest *Manifest) (*Verifier, error) {
	if err := ValidateRPID(rpID); err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, fmt.Errorf("%w: manifest is required", ErrManifest)
	}
	return &Verifier{
		rpID:     rpID,
		rpIDHash: sha256.Sum256([]byte(rpID)),
		manifest: manifest,
	}, nil
}

// RPID returns the configured relying party identifier.
func (v *Verifier) RPID() string {
	return v.rpID
}

// Manifest returns the configured origin manifest.
func (v *Verifier) Manifest() *Manifest {
	return v.manifest
}

// VerifyAssertion applies the full origin and RP ID policy to one assertion:
//
//  1. The authenticator data's rpIdHash must equal SHA-256(RPID).
//  2. clientData.origin must match an entry in the manifest exactly.
//  3. When crossOrigin is true or topOrigin is present, clientData.topOrigin
//     must also match a manifest entry.
//
// Exact matching means an allow-listed brand origin never accidentally
// authorizes a look-alike domain or an unrelated subdomain.
func (v *Verifier) VerifyAssertion(req AssertionRequest) error {
	if err := v.VerifyRPIDHash(req.AuthenticatorData); err != nil {
		return err
	}

	origin := req.ClientData.Origin
	if !v.manifest.Allows(origin) {
		return fmt.Errorf("%w: %q", ErrOriginNotAllowed, origin)
	}

	// A top-level cross-origin credential use (crossOrigin) and any frame
	// carrying an explicit topOrigin both require an independently
	// allow-listed top-level document.
	if req.ClientData.CrossOrigin || req.ClientData.TopOrigin != "" {
		topOrigin := req.ClientData.TopOrigin
		if topOrigin == "" {
			return fmt.Errorf("%w: crossOrigin=true without topOrigin", ErrTopOriginRequired)
		}
		if !v.manifest.Allows(topOrigin) {
			return fmt.Errorf("%w: top origin %q", ErrTopOriginRequired, topOrigin)
		}
	}
	return nil
}

// VerifyRPIDHash checks that authenticatorData is well formed and that its
// leading 32 bytes equal the hash of the configured RP ID. The comparison
// uses constant time so an attacker cannot use response timing to probe for a
// matching hash.
func (v *Verifier) VerifyRPIDHash(authenticatorData []byte) error {
	if len(authenticatorData) < sha256.Size {
		return fmt.Errorf("%w: authenticator data is %d bytes, need at least %d",
			ErrRPIDHash, len(authenticatorData), sha256.Size)
	}
	if subtle.ConstantTimeCompare(authenticatorData[:sha256.Size], v.rpIDHash[:]) != 1 {
		return ErrRPIDHash
	}
	return nil
}

// ExpectedRPIDHash returns the SHA-256 hash of the configured RP ID.
func (v *Verifier) ExpectedRPIDHash() [sha256.Size]byte {
	return v.rpIDHash
}

// RPIDHash computes SHA-256(rpID), the value a WebAuthn authenticator stores
// in the first 32 bytes of its authenticator data.
func RPIDHash(rpID string) [sha256.Size]byte {
	return sha256.Sum256([]byte(rpID))
}

// ParseClientData decodes the base64url CollectedClientData bytes returned by
// an authenticator into the subset used for origin policy.
func ParseClientData(encoded []byte) (ClientData, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(encoded))
	if err != nil {
		// Some clients emit standard base64 with padding; accept it as well.
		raw, err = base64.StdEncoding.DecodeString(string(encoded))
		if err != nil {
			return ClientData{}, fmt.Errorf("webauthn: decoding clientDataJSON: %w", err)
		}
	}
	var data ClientData
	if err := parseClientJSON(raw, &data); err != nil {
		return ClientData{}, err
	}
	return data, nil
}
