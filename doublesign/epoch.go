package doublesign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
)

const epochDomain = "BFT-AUDIT/V1/EPOCH"

// Member is one validator in an epoch. Member identities are fixed IDs bound
// to an Ed25519 key and voting weight for the epoch.
type Member struct {
	ID     string            `json:"id"`
	PubKey ed25519.PublicKey `json:"pubKey"`
	Weight uint64            `json:"weight"`
}

// EpochSet is the immutable validator membership of one epoch.
type EpochSet struct {
	Epoch   uint64   `json:"epoch"`
	ChainID string   `json:"chainID"`
	Members []Member `json:"members"`
}

// RootBytes is the canonical byte string committed by Root.
func RootBytes(e EpochSet) []byte {
	b := appendString(nil, epochDomain)
	b = appendString(b, e.ChainID)
	b = appendU64(b, e.Epoch)
	b = appendU64(b, uint64(len(e.Members)))
	for _, m := range e.Members {
		b = appendString(b, m.ID)
		b = appendBytes(b, m.PubKey)
		b = appendU64(b, m.Weight)
	}
	return b
}

// Root is the signed anchor digest of an epoch validator set.
func Root(e EpochSet) []byte {
	h := sha256.Sum256(RootBytes(e))
	return h[:]
}

// AuthorityAnchor binds an epoch root to the governance authority signature.
// Carrying this inside a conviction lets an offline verifier confirm that the
// validator set itself was authorized, without any host database.
type AuthorityAnchor struct {
	Epoch       uint64 `json:"epoch"`
	ChainID     string `json:"chainID"`
	Root        []byte `json:"root"`
	AuthorityID string `json:"authorityID"`
	Signature   []byte `json:"signature"`
}

// AnchorRoot signs an epoch root with the authority key.
func AnchorRoot(authorityID string, priv ed25519.PrivateKey, e EpochSet) AuthorityAnchor {
	root := Root(e)
	return AuthorityAnchor{
		Epoch:       e.Epoch,
		ChainID:     e.ChainID,
		Root:        root,
		AuthorityID: authorityID,
		Signature:   ed25519.Sign(priv, root),
	}
}

// Verify checks the authority signature and that the anchor matches the set.
func (a AuthorityAnchor) Verify(e EpochSet, authorityPub ed25519.PublicKey) error {
	if a.Epoch != e.Epoch || a.ChainID != e.ChainID {
		return errors.New("anchor does not match epoch set")
	}
	if !bytesEqual(a.Root, Root(e)) {
		return errors.New("anchor root mismatch")
	}
	if len(a.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(authorityPub, a.Root, a.Signature) {
		return errors.New("invalid authority signature")
	}
	return nil
}

// Validate checks internal consistency of an epoch set.
func (e EpochSet) Validate() error {
	if e.ChainID == "" {
		return errors.New("empty chain id")
	}
	if len(e.Members) == 0 {
		return errors.New("epoch has no members")
	}
	seen := map[string]bool{}
	for _, m := range e.Members {
		if m.ID == "" {
			return errors.New("member with empty id")
		}
		if seen[m.ID] {
			return errors.New("duplicate member id: " + m.ID)
		}
		seen[m.ID] = true
		if len(m.PubKey) != ed25519.PublicKeySize {
			return errors.New("bad public key for " + m.ID)
		}
		if m.Weight == 0 {
			return errors.New("zero weight for " + m.ID)
		}
	}
	return nil
}

func (e EpochSet) member(id string) (Member, bool) {
	for _, m := range e.Members {
		if m.ID == id {
			return m, true
		}
	}
	return Member{}, false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
