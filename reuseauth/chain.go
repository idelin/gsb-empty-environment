package reuseauth

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"net/url"
)

// Hop is the signed claim for one HTTP redirect or terminal response.
type Hop struct {
	RequestURL string `json:"request_url"`
	Status     int    `json:"status"`
	Location   string `json:"location,omitempty"`
	Responder  Origin `json:"responder"`
	NotBefore  int64  `json:"not_before,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

func (h Hop) hash() Hash {
	return hashString("reuseauth.hop.v1", string(mustJSON(h)))
}

// SignedHop is a hop claim plus the origin key signature.
type SignedHop struct {
	Hop
	SignerID  SignerID `json:"signer_id"`
	Signature []byte   `json:"signature"`
}

// HopSigner pairs an origin signer identity with its Ed25519 key.
type HopSigner struct {
	ID  SignerID
	Key ed25519.PrivateKey
}

func hopSignatureDigest(index int, previous Hash, hop Hop) Hash {
	var indexBytes [8]byte
	binary.BigEndian.PutUint64(indexBytes[:], uint64(index))
	hopHash := hop.hash()
	return hashParts("reuseauth.hop-signature.v1", indexBytes[:], previous[:], hopHash[:])
}

// SignHop signs one hop in a chain. Most callers should use
// SignRedirectChain so linkage is computed correctly.
func SignHop(signer HopSigner, index int, previous Hash, hop Hop) (SignedHop, error) {
	if signer.ID == "" || len(signer.Key) != ed25519.PrivateKeySize {
		return SignedHop{}, fmt.Errorf("%w: valid Ed25519 signer required", ErrInvalidArgument)
	}
	digest := hopSignatureDigest(index, previous, hop)
	return SignedHop{Hop: hop, SignerID: signer.ID, Signature: ed25519.Sign(signer.Key, digest[:])}, nil
}

// SignedRedirectChain is an ordered, signature-linked redirect history.
type SignedRedirectChain struct {
	Hops []SignedHop `json:"hops"`
}

// SignRedirectChain signs hops with one signer per hop.
func SignRedirectChain(hops []Hop, signers []HopSigner) (SignedRedirectChain, error) {
	if len(hops) == 0 {
		return SignedRedirectChain{}, fmt.Errorf("%w: empty redirect chain", ErrInvalidArgument)
	}
	if len(hops) != len(signers) {
		return SignedRedirectChain{}, fmt.Errorf("%w: %d hops but %d signers", ErrInvalidArgument, len(hops), len(signers))
	}
	signed := make([]SignedHop, 0, len(hops))
	var previous Hash
	for i, hop := range hops {
		sh, err := SignHop(signers[i], i, previous, hop)
		if err != nil {
			return SignedRedirectChain{}, err
		}
		signed = append(signed, sh)
		previous = hop.hash()
	}
	return SignedRedirectChain{Hops: signed}, nil
}

// TrustStore maps signing identities to trusted keys and origin names.
type TrustStore struct {
	keys    map[SignerID]ed25519.PublicKey
	origins map[SignerID]Origin
}

// NewTrustStore creates an empty origin key store.
func NewTrustStore() *TrustStore {
	return &TrustStore{keys: make(map[SignerID]ed25519.PublicKey), origins: make(map[SignerID]Origin)}
}

// Add trusts id for origin.
func (t *TrustStore) Add(id SignerID, key ed25519.PublicKey, origin Origin) error {
	if t == nil || id == "" || len(key) != ed25519.PublicKeySize || origin.IsZero() {
		return fmt.Errorf("%w: valid signer id, key, and origin required", ErrInvalidArgument)
	}
	t.keys[id] = append(ed25519.PublicKey(nil), key...)
	t.origins[id] = origin
	return nil
}

func (t *TrustStore) verifySignature(id SignerID, origin Origin, digest Hash, signature []byte) error {
	if t == nil {
		return fmt.Errorf("%w: nil trust store", ErrSignature)
	}
	key, ok := t.keys[id]
	if !ok {
		return fmt.Errorf("%w: unknown signer %q", ErrSignature, id)
	}
	boundOrigin, ok := t.origins[id]
	if !ok || !boundOrigin.Equal(origin) {
		return fmt.Errorf("%w: signer %q is not bound to %s", ErrSignature, id, origin)
	}
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, digest[:], signature) {
		return fmt.Errorf("%w: signer %q", ErrSignature, id)
	}
	return nil
}

// Verify validates signatures, time windows, URL/origin binding, redirect
// linkage, and terminal status. It returns the canonical chain digest.
func (c SignedRedirectChain) Verify(trust *TrustStore, now int64) (Hash, error) {
	if len(c.Hops) == 0 {
		return Hash{}, fmt.Errorf("%w: empty chain", ErrChain)
	}
	var previous Hash
	digests := make([][]byte, 0, len(c.Hops))
	for i, sh := range c.Hops {
		if err := validateHopShape(sh.Hop); err != nil {
			return Hash{}, err
		}
		if now != 0 {
			if sh.NotBefore != 0 && now < sh.NotBefore {
				return Hash{}, fmt.Errorf("%w: hop %d not active", ErrChain, i)
			}
			if sh.ExpiresAt != 0 && now > sh.ExpiresAt {
				return Hash{}, fmt.Errorf("%w: hop %d expired", ErrChain, i)
			}
		}
		requestOrigin, err := OriginFromURL(sh.RequestURL)
		if err != nil {
			return Hash{}, fmt.Errorf("%w: hop %d: %v", ErrChain, i, err)
		}
		if !requestOrigin.Equal(sh.Responder) {
			return Hash{}, fmt.Errorf("%w: hop %d responder %s does not match URL origin %s", ErrChain, i, sh.Responder, requestOrigin)
		}
		digest := hopSignatureDigest(i, previous, sh.Hop)
		if err := trust.verifySignature(sh.SignerID, sh.Responder, digest, sh.Signature); err != nil {
			return Hash{}, fmt.Errorf("hop %d: %w", i, err)
		}

		if i < len(c.Hops)-1 {
			if sh.Status < 300 || sh.Status >= 400 {
				return Hash{}, fmt.Errorf("%w: hop %d status %d is not a redirect", ErrChain, i, sh.Status)
			}
			if sh.Location == "" {
				return Hash{}, fmt.Errorf("%w: hop %d missing location", ErrChain, i)
			}
			current, _ := url.Parse(sh.RequestURL)
			location, err := url.Parse(sh.Location)
			if err != nil {
				return Hash{}, fmt.Errorf("%w: hop %d location: %v", ErrChain, i, err)
			}
			resolved := current.ResolveReference(location).String()
			if resolved != c.Hops[i+1].RequestURL {
				return Hash{}, fmt.Errorf("%w: hop %d location %q does not link to %q", ErrChain, i, resolved, c.Hops[i+1].RequestURL)
			}
		} else if sh.Status < 200 || sh.Status >= 300 {
			return Hash{}, fmt.Errorf("%w: terminal status %d", ErrChain, sh.Status)
		}

		signedDigest := hashParts("reuseauth.signed-hop.v1", mustJSON(sh))
		digests = append(digests, signedDigest[:])
		previous = sh.Hop.hash()
	}
	return hashParts("reuseauth.chain.v1", digests...), nil
}

func validateHopShape(h Hop) error {
	if h.RequestURL == "" || h.Status == 0 || h.Responder.IsZero() {
		return fmt.Errorf("%w: hop url, status, and responder are required", ErrInvalidArgument)
	}
	u, err := url.Parse(h.RequestURL)
	if err != nil || !u.IsAbs() {
		return fmt.Errorf("%w: invalid absolute request URL %q", ErrInvalidArgument, h.RequestURL)
	}
	return nil
}

// FinalResponse is the signed terminal response claim.
type FinalResponse struct {
	FinalURL       string `json:"final_url"`
	Status         int    `json:"status"`
	Responder      Origin `json:"responder"`
	Recipient      Origin `json:"recipient"`
	HeaderDigest   Hash   `json:"header_digest"`
	ManifestDigest Hash   `json:"manifest_digest"`
	Length         int64  `json:"length"`
	Public         bool   `json:"public"`
	NotBefore      int64  `json:"not_before,omitempty"`
	ExpiresAt      int64  `json:"expires_at,omitempty"`
}

func (r FinalResponse) hash() Hash {
	return hashString("reuseauth.final-response.v1", string(mustJSON(r)))
}

// SignedResponse is an Ed25519-signed FinalResponse.
type SignedResponse struct {
	Response  FinalResponse `json:"response"`
	SignerID  SignerID      `json:"signer_id"`
	Signature []byte        `json:"signature"`
}

// SignResponse signs r with signer.
func SignResponse(signer HopSigner, r FinalResponse) (SignedResponse, error) {
	if signer.ID == "" || len(signer.Key) != ed25519.PrivateKeySize {
		return SignedResponse{}, fmt.Errorf("%w: valid Ed25519 signer required", ErrInvalidArgument)
	}
	digest := r.hash()
	return SignedResponse{Response: r, SignerID: signer.ID, Signature: ed25519.Sign(signer.Key, digest[:])}, nil
}

// Verify checks the response signature, audience, time, and linkage to the
// terminal chain hop.
func (sr SignedResponse) Verify(trust *TrustStore, chain SignedRedirectChain, now int64) (Hash, error) {
	if len(chain.Hops) == 0 {
		return Hash{}, fmt.Errorf("%w: response without chain", ErrChain)
	}
	r := sr.Response
	last := chain.Hops[len(chain.Hops)-1]
	if r.FinalURL != last.RequestURL || r.Status != last.Status || !r.Responder.Equal(last.Responder) {
		return Hash{}, fmt.Errorf("%w: final response does not bind terminal hop", ErrChain)
	}
	responseOrigin, err := OriginFromURL(r.FinalURL)
	if err != nil {
		return Hash{}, err
	}
	if !responseOrigin.Equal(r.Responder) {
		return Hash{}, fmt.Errorf("%w: response responder does not match final URL", ErrOrigin)
	}
	if !r.Public {
		if r.Recipient.IsZero() || r.Recipient.IsAny() {
			return Hash{}, fmt.Errorf("%w: private response requires exact signed recipient", ErrPublicFlag)
		}
	}
	if r.Length < -1 {
		return Hash{}, fmt.Errorf("%w: invalid response length %d", ErrLength, r.Length)
	}
	if now != 0 {
		if r.NotBefore != 0 && now < r.NotBefore {
			return Hash{}, fmt.Errorf("%w: response not active", ErrChain)
		}
		if r.ExpiresAt != 0 && now > r.ExpiresAt {
			return Hash{}, fmt.Errorf("%w: response expired", ErrChain)
		}
	}
	digest := r.hash()
	if err := trust.verifySignature(sr.SignerID, r.Responder, digest, sr.Signature); err != nil {
		return Hash{}, err
	}
	return hashParts("reuseauth.signed-response.v1", mustJSON(sr)), nil
}
