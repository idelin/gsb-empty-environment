package reuseauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

const proofVersion = "reuse-proof-v1"

// RequestContext is the browser request context bound into a reuse proof.
type RequestContext struct {
	Method         string         `json:"method"`
	URL            string         `json:"url"`
	Source         Origin         `json:"source"`
	CredentialMode CredentialMode `json:"credential_mode"`
	TopLevelSite   string         `json:"top_level_site"`
	RangeHeader    string         `json:"range_header,omitempty"`
	HeaderDigest   Hash           `json:"header_digest"`
}

func (c RequestContext) normalize(nav Navigation) (RequestContext, error) {
	if c.Method == "" {
		c.Method = "GET"
	}
	c.Method = strings.ToUpper(c.Method)
	if c.URL == "" {
		return RequestContext{}, fmt.Errorf("%w: request URL required", ErrInvalidArgument)
	}
	u, err := url.Parse(c.URL)
	if err != nil || !u.IsAbs() {
		return RequestContext{}, fmt.Errorf("%w: absolute request URL required", ErrInvalidArgument)
	}
	if c.Source.IsZero() {
		c.Source = nav.Recipient
	} else if !c.Source.Equal(nav.Recipient) {
		return RequestContext{}, fmt.Errorf("%w: context source %s does not match navigation recipient %s", ErrOrigin, c.Source, nav.Recipient)
	}
	if c.CredentialMode == "" {
		c.CredentialMode = nav.CredentialMode
	} else if c.CredentialMode != nav.CredentialMode {
		return RequestContext{}, fmt.Errorf("%w: context credential mode %s does not match navigation %s", ErrPolicyDenied, c.CredentialMode, nav.CredentialMode)
	}
	if c.TopLevelSite == "" {
		c.TopLevelSite = nav.TopLevelSite
	} else if c.TopLevelSite != nav.TopLevelSite {
		return RequestContext{}, fmt.Errorf("%w: context site %q does not match navigation %q", ErrEpoch, c.TopLevelSite, nav.TopLevelSite)
	}
	return c, nil
}

func (c RequestContext) hash() Hash {
	return hashString("reuseauth.request-context.v1", string(mustJSON(c)))
}

// ReuseRequest contains all evidence needed to authorize a cache reuse.
type ReuseRequest struct {
	RequestID RequestID      `json:"request_id"`
	Context   RequestContext `json:"context"`
	// Offered contains this navigation's segment/range representation digests.
	Offered  []RangeDigest       `json:"offered,omitempty"`
	Now      int64               `json:"now"`
	Chain    SignedRedirectChain `json:"chain"`
	Response SignedResponse      `json:"response"`
	Manifest Manifest            `json:"manifest"`
}

// Proof is the non-repudiable same-generation reuse authorization statement.
type Proof struct {
	Version        string        `json:"version"`
	AuthorityID    SignerID      `json:"authority_id"`
	RequestID      RequestID     `json:"request_id"`
	NavigationID   NavID         `json:"navigation_id"`
	Epoch          Epoch         `json:"epoch"`
	PolicySequence uint64        `json:"policy_sequence"`
	PolicyHistory  Hash          `json:"policy_history"`
	RequestContext Hash          `json:"request_context"`
	Chain          Hash          `json:"chain"`
	FinalResponse  Hash          `json:"final_response"`
	Manifest       Hash          `json:"manifest"`
	Offered        Hash          `json:"offered"`
	Shared         []RangeDigest `json:"shared,omitempty"`
	IssuedAt       int64         `json:"issued_at"`
}

func (p Proof) signingDigest() Hash {
	return hashString("reuseauth.proof.v1", string(mustJSON(p)))
}

// SealedProof is an Ed25519-signed Proof.
type SealedProof struct {
	Proof     Proof  `json:"proof"`
	Signature []byte `json:"signature"`
}

// Authority mints and verifies generation-bound reuse proofs.
type Authority struct {
	id    SignerID
	trust *TrustStore
	priv  ed25519.PrivateKey
	pub   ed25519.PublicKey
}

// NewAuthority creates a signing authority.
func NewAuthority(id SignerID, trust *TrustStore, priv ed25519.PrivateKey) (*Authority, error) {
	if id == "" || trust == nil || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: id, trust store, and Ed25519 private key required", ErrInvalidArgument)
	}
	return &Authority{id: id, trust: trust, priv: append(ed25519.PrivateKey(nil), priv...), pub: priv.Public().(ed25519.PublicKey)}, nil
}

// NewAuthorityFromSeed deterministically creates an authority from a 32-byte seed.
func NewAuthorityFromSeed(id SignerID, trust *TrustStore, seed []byte) (*Authority, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: authority seed must be %d bytes", ErrInvalidArgument, ed25519.SeedSize)
	}
	return NewAuthority(id, trust, ed25519.NewKeyFromSeed(seed))
}

// NewPublicAuthority creates a verifier that cannot mint proofs.
func NewPublicAuthority(id SignerID, trust *TrustStore, pub ed25519.PublicKey) (*Authority, error) {
	if id == "" || trust == nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: id, trust store, and Ed25519 public key required", ErrInvalidArgument)
	}
	return &Authority{id: id, trust: trust, pub: append(ed25519.PublicKey(nil), pub...)}, nil
}

// GenerateAuthority is a convenience for tests and first-run hosts.
func GenerateAuthority(id SignerID, trust *TrustStore) (*Authority, ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	a, err := NewAuthority(id, trust, priv)
	if err != nil {
		return nil, nil, err
	}
	return a, priv, nil
}

// PublicKey returns the authority's public verification key.
func (a *Authority) PublicKey() ed25519.PublicKey { return append(ed25519.PublicKey(nil), a.pub...) }

// AuthorizeReuse verifies all evidence and mints a proof if content, request
// context, final response, and policy are authorized in nav's current epoch.
func (a *Authority) AuthorizeReuse(nav Navigation, req ReuseRequest) (SealedProof, []RangeDigest, error) {
	if a == nil || a.priv == nil {
		return SealedProof{}, nil, fmt.Errorf("%w: signing authority required", ErrInvalidArgument)
	}
	if req.RequestID == "" {
		return SealedProof{}, nil, fmt.Errorf("%w: request id required", ErrInvalidArgument)
	}
	ctx, err := req.Context.normalize(nav)
	if err != nil {
		return SealedProof{}, nil, err
	}
	if err := req.Manifest.Validate(); err != nil {
		return SealedProof{}, nil, err
	}
	chainDigest, err := req.Chain.Verify(a.trust, req.Now)
	if err != nil {
		return SealedProof{}, nil, err
	}
	responseDigest, err := req.Response.Verify(a.trust, req.Chain, req.Now)
	if err != nil {
		return SealedProof{}, nil, err
	}
	resp := req.Response.Response
	if resp.ManifestDigest != req.Manifest.Digest() {
		return SealedProof{}, nil, fmt.Errorf("%w: response manifest digest does not match manifest", ErrDigestMismatch)
	}
	if resp.Length >= 0 && req.Manifest.ObjectLength >= 0 && resp.Length != req.Manifest.ObjectLength {
		return SealedProof{}, nil, fmt.Errorf("%w: response length %d != manifest length %d", ErrLength, resp.Length, req.Manifest.ObjectLength)
	}
	if !resp.Public && !resp.Recipient.Equal(nav.Recipient) {
		return SealedProof{}, nil, fmt.Errorf("%w: private response audience %s does not match %s", ErrOrigin, resp.Recipient, nav.Recipient)
	}
	if resp.Public && !resp.Recipient.IsZero() && !resp.Recipient.IsAny() && !resp.Recipient.Equal(nav.Recipient) {
		return SealedProof{}, nil, fmt.Errorf("%w: signed public response audience %s does not match %s", ErrOrigin, resp.Recipient, nav.Recipient)
	}
	if err := nav.Check(PolicyQuery{Action: ActionRead, Source: ctx.Source, Target: resp.Responder, Public: resp.Public}); err != nil {
		return SealedProof{}, nil, err
	}

	shared, err := EquivalentRanges(req.Manifest.RangeDigests(), req.Offered)
	if err != nil {
		return SealedProof{}, nil, err
	}
	policySequence := uint64(len(nav.History))
	policyDigest, err := policyPrefixHash(nav.History, policySequence)
	if err != nil {
		return SealedProof{}, nil, err
	}

	proof := Proof{
		Version:        proofVersion,
		AuthorityID:    a.id,
		RequestID:      req.RequestID,
		NavigationID:   nav.ID,
		Epoch:          nav.Epoch,
		PolicySequence: policySequence,
		PolicyHistory:  policyDigest,
		RequestContext: ctx.hash(),
		Chain:          chainDigest,
		FinalResponse:  responseDigest,
		Manifest:       req.Manifest.Digest(),
		Offered:        hashRangeDigests("reuseauth.offered.v1", req.Offered),
		Shared:         shared,
		IssuedAt:       req.Now,
	}
	digest := proof.signingDigest()
	return SealedProof{Proof: proof, Signature: ed25519.Sign(a.priv, digest[:])}, shared, nil
}

// VerifyProof independently verifies the signature and re-evaluates the proof
// against the navigation's current generation and policy history.
func (a *Authority) VerifyProof(nav Navigation, sealed SealedProof, req ReuseRequest, now int64) ([]RangeDigest, error) {
	if a == nil || len(a.pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: public authority required", ErrSignature)
	}
	p := sealed.Proof
	if p.Version != proofVersion || p.AuthorityID != a.id {
		return nil, fmt.Errorf("%w: unknown proof version or authority", ErrSignature)
	}
	digest := p.signingDigest()
	if len(sealed.Signature) != ed25519.SignatureSize || !ed25519.Verify(a.pub, digest[:], sealed.Signature) {
		return nil, fmt.Errorf("%w: proof signature", ErrSignature)
	}
	if p.NavigationID != nav.ID {
		return nil, fmt.Errorf("%w: proof navigation %s != %s", ErrOrigin, p.NavigationID, nav.ID)
	}
	if p.Epoch != nav.Epoch || nav.Revoked {
		return nil, fmt.Errorf("stale proof generation: proof epoch %d, current %d, revoked=%v: %w", p.Epoch, nav.Epoch, nav.Revoked, ErrEpoch)
	}
	prefixDigest, err := policyPrefixHash(nav.History, p.PolicySequence)
	if err != nil {
		return nil, err
	}
	if prefixDigest != p.PolicyHistory {
		return nil, fmt.Errorf("%w: policy history prefix changed", ErrDigestMismatch)
	}
	ctx, err := req.Context.normalize(nav)
	if err != nil {
		return nil, err
	}
	if ctx.hash() != p.RequestContext {
		return nil, fmt.Errorf("%w: request context changed", ErrDigestMismatch)
	}
	if hashRangeDigests("reuseauth.offered.v1", req.Offered) != p.Offered {
		return nil, fmt.Errorf("%w: offered representations changed", ErrDigestMismatch)
	}
	if err := req.Manifest.Validate(); err != nil {
		return nil, err
	}
	verifyNow := now
	if verifyNow == 0 {
		verifyNow = req.Now
	}
	chainDigest, err := req.Chain.Verify(a.trust, verifyNow)
	if err != nil {
		return nil, err
	}
	if chainDigest != p.Chain {
		return nil, fmt.Errorf("%w: chain digest changed", ErrDigestMismatch)
	}
	responseDigest, err := req.Response.Verify(a.trust, req.Chain, verifyNow)
	if err != nil {
		return nil, err
	}
	if responseDigest != p.FinalResponse {
		return nil, fmt.Errorf("%w: response digest changed", ErrDigestMismatch)
	}
	if req.Manifest.Digest() != p.Manifest || req.Response.Response.ManifestDigest != p.Manifest {
		return nil, fmt.Errorf("%w: manifest digest changed", ErrDigestMismatch)
	}
	if hashRangeDigests("reuseauth.offered.v1", req.Offered) != p.Offered {
		return nil, fmt.Errorf("%w: offered representations changed", ErrDigestMismatch)
	}
	shared, err := EquivalentRanges(req.Manifest.RangeDigests(), req.Offered)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(shared, p.Shared) {
		return nil, fmt.Errorf("%w: shared range set changed", ErrDigestMismatch)
	}
	resp := req.Response.Response
	if err := nav.Check(PolicyQuery{Action: ActionRead, Source: ctx.Source, Target: resp.Responder, Public: resp.Public}); err != nil {
		return nil, err
	}
	return shared, nil
}
