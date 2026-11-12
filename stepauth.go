package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// KeyTypeEd25519 is the only supported functionary key type.
const KeyTypeEd25519 = "ed25519"

var (
	// ErrLayoutExpired is returned when the layout is past its expiration.
	ErrLayoutExpired = errors.New("stepauth: layout expired")
	// ErrThresholdNotMet is returned when too few distinct functionaries
	// with agreeing link digests authorized the step.
	ErrThresholdNotMet = errors.New("stepauth: signature threshold not met")
)

// Key is a functionary public key as listed in the layout.
type Key struct {
	KeyID string
	Type  string
	Value []byte // raw public key bytes
}

// Fingerprint returns the canonical identity of the key material.
// Alias keys (different KeyIDs wrapping the same public key) share a
// fingerprint and are therefore merged during deduplication.
func (k Key) Fingerprint() string {
	sum := sha256.Sum256(append([]byte(k.Type+"\x00"), k.Value...))
	return hex.EncodeToString(sum[:])
}

// Layout describes the authorization policy for one supply-chain step.
type Layout struct {
	StepName  string
	Keys      []Key // expected functionary keys
	Threshold int   // minimum number of distinct agreeing functionaries
	Expires   time.Time
}

// Signature is a single signature attached to a link.
type Signature struct {
	KeyID string
	Value []byte
}

// Link is the in-toto link metadata for one step execution.
type Link struct {
	Name       string
	Materials  map[string]string // artifact URI -> digest
	Products   map[string]string // artifact URI -> digest
	Signatures []Signature
}

// linkPayload is the canonical signed content of a link.
type linkPayload struct {
	Name      string            `json:"name"`
	Materials map[string]string `json:"materials"`
	Products  map[string]string `json:"products"`
}

// signingBytes returns the canonical bytes that functionaries sign.
// encoding/json sorts map keys, so the encoding is deterministic.
func (l Link) signingBytes() []byte {
	b, _ := json.Marshal(linkPayload{
		Name:      l.Name,
		Materials: l.Materials,
		Products:  l.Products,
	})
	return b
}

// Digest identifies the exact materials/products a signer attested to.
func (l Link) Digest() string {
	sum := sha256.Sum256(l.signingBytes())
	return hex.EncodeToString(sum[:])
}

// VerifyStep checks that the step named in the layout was authorized by at
// least Threshold distinct functionaries whose links agree on materials and
// products. It returns the sorted KeyIDs of the counted signers.
//
// Rules:
//   - an expired layout rejects everything;
//   - signatures are deduplicated by key fingerprint, so replays of the same
//     private key and alias KeyIDs over identical key material count once;
//   - signatures from keys outside the layout set are ignored;
//   - only the largest group of signers attesting to identical link digests
//     counts toward the threshold.
func VerifyStep(layout Layout, links []Link, now time.Time) ([]string, error) {
	if !now.Before(layout.Expires) {
		return nil, fmt.Errorf("%w: step %q (expired %s)",
			ErrLayoutExpired, layout.StepName, layout.Expires.UTC().Format(time.RFC3339))
	}

	keysByID := make(map[string]Key, len(layout.Keys))
	for _, k := range layout.Keys {
		if _, dup := keysByID[k.KeyID]; !dup {
			keysByID[k.KeyID] = k
		}
	}

	// fingerprint -> link digest attested by that distinct key
	attested := make(map[string]string)
	keyIDByFP := make(map[string]string)

	for _, link := range links {
		if link.Name != layout.StepName {
			continue
		}
		payload := link.signingBytes()
		digest := link.Digest()
		for _, sig := range link.Signatures {
			key, ok := keysByID[sig.KeyID]
			if !ok {
				continue // signer not authorized by the layout
			}
			if key.Type != KeyTypeEd25519 || !ed25519.Verify(ed25519.PublicKey(key.Value), payload, sig.Value) {
				continue // unsupported key or invalid signature
			}
			fp := key.Fingerprint()
			if _, seen := attested[fp]; seen {
				continue // same key material already counted
			}
			attested[fp] = digest
			keyIDByFP[fp] = sig.KeyID
		}
	}

	// Group distinct signers by the digest they attested to; only the
	// largest agreeing group may satisfy the threshold.
	type group struct {
		count  int
		keyIDs []string
	}
	groups := make(map[string]*group)
	for fp, digest := range attested {
		g := groups[digest]
		if g == nil {
			g = &group{}
			groups[digest] = g
		}
		g.count++
		g.keyIDs = append(g.keyIDs, keyIDByFP[fp])
	}

	best := &group{}
	for _, g := range groups {
		if g.count > best.count {
			best = g
		}
	}
	if best.count < layout.Threshold {
		return nil, fmt.Errorf("%w: step %q needs %d, largest agreeing group has %d",
			ErrThresholdNotMet, layout.StepName, layout.Threshold, best.count)
	}
	sort.Strings(best.keyIDs)
	return best.keyIDs, nil
}
