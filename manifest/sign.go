package manifest

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
)

// SignManifest signs the canonical encoding of m with the Ed25519 private key
// and returns a root attestation for rootID. It is a convenience for hosts and
// test harnesses; admission only ever consumes the resulting signature.
func SignManifest(m *Manifest, rootID string, priv ed25519.PrivateKey) RootSignature {
	return RootSignature{
		RootID:    rootID,
		Signature: ed25519.Sign(priv, CanonicalEncoding(m)),
	}
}

// SignFirmware signs the firmware signing bytes for img and returns a
// firmware signature attributable to keyID.
func SignFirmware(img *FirmwareImage, keyID string, priv ed25519.PrivateKey) FirmwareSignature {
	return FirmwareSignature{
		KeyID:     keyID,
		Signature: ed25519.Sign(priv, FirmwareSigningBytes(img)),
	}
}

// verifyRootQuorum validates every attestation against the pinned root trust
// set and returns the number of distinct, known roots whose signatures verify.
//
// Semantics:
//   - unknown roots are never counted and never veto acceptance on their own;
//   - a malformed signature or a signature that fails verification under a
//     known root is a hard failure;
//   - two different signatures for the same root ID are a conflict;
//   - an exact duplicate attestation is a hard failure as well, since a
//     well-formed submission carries one attestation per root.
func verifyRootQuorum(sm *SignedManifest, roots map[string]Root) (int, error) {
	seen := make(map[string][]byte) // root ID -> first signature bytes
	var unknown []string

	for _, sig := range sm.Signatures {
		root, known := roots[sig.RootID]
		if !known {
			unknown = append(unknown, sig.RootID)
			continue
		}
		if prior, ok := seen[sig.RootID]; ok {
			// A repeat attestation from one root is classified by content
			// before cryptography: differing bytes are an equivocation
			// conflict, identical bytes are a duplicate submission. Both are
			// rejected, regardless of whether the second value also verifies.
			if !bytes.Equal(prior, sig.Signature) {
				return 0, fmt.Errorf("%w: root %q", ErrRootConflict, sig.RootID)
			}
			return 0, fmt.Errorf("%w: root %q", ErrDuplicateRoot, sig.RootID)
		}
		if len(sig.Signature) != ed25519.SignatureSize ||
			!ed25519.Verify(ed25519.PublicKey(root.PubKey), CanonicalEncoding(sm.Manifest), sig.Signature) {
			return 0, fmt.Errorf("%w: root %q", ErrRootSignature, sig.RootID)
		}
		seen[sig.RootID] = append([]byte(nil), sig.Signature...)
	}

	if len(seen) == 0 && len(unknown) > 0 {
		return 0, fmt.Errorf("%w: %v", ErrUnknownRoot, unknown)
	}
	return len(seen), nil
}

// eligibleFirmwareKeys returns the manifest entries that may sign firmware for
// the given manifest generation: unrevoked, purpose list containing
// PurposeFirmware, and generation inside the entry window (closed start, open
// stop).
func eligibleFirmwareKeys(m *Manifest) map[string]KeyEntry {
	revoked := make(map[string]struct{}, len(m.RevokedKeyIDs))
	for _, id := range m.RevokedKeyIDs {
		revoked[id] = struct{}{}
	}

	out := make(map[string]KeyEntry)
	for _, e := range m.Entries {
		if _, isRevoked := revoked[e.KeyID]; isRevoked {
			continue
		}
		if !containsString(e.Purposes, PurposeFirmware) {
			continue
		}
		if m.Generation < e.GenStart || m.Generation >= e.GenStop {
			continue
		}
		out[e.KeyID] = e
	}
	return out
}

// EligibleFirmwareKeys exposes the admission view of a manifest: the key IDs
// eligible to sign firmware at the manifest's own generation.
func EligibleFirmwareKeys(m *Manifest) []string {
	eligible := eligibleFirmwareKeys(m)
	ids := make([]string, 0, len(eligible))
	for id := range eligible {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// verifyFirmware checks firmware signatures against the accepted manifest and
// returns the number of distinct eligible public keys that verified.
//
// Multiple key IDs that share identical public key material are aliases and
// count as a single signer. Signatures from ineligible, unknown or revoked key
// IDs are rejected outright rather than silently ignored.
func verifyFirmware(img *FirmwareImage, m *Manifest) (int, error) {
	eligible := eligibleFirmwareKeys(m)
	msg := FirmwareSigningBytes(img)

	// keyID -> first signature seen, for duplicate/conflict reporting.
	seenSig := make(map[string][]byte)
	// distinct verified public key material, the actual quorum counter.
	distinctKeys := make(map[string]struct{})

	for _, fs := range img.Signatures {
		entry, ok := eligible[fs.KeyID]
		if !ok {
			return 0, fmt.Errorf("%w: key %q not eligible", ErrFirmwareSignature, fs.KeyID)
		}
		if len(fs.Signature) != ed25519.SignatureSize ||
			!ed25519.Verify(ed25519.PublicKey(entry.PubKey), msg, fs.Signature) {
			return 0, fmt.Errorf("%w: key %q", ErrFirmwareSignature, fs.KeyID)
		}
		if prior, dup := seenSig[fs.KeyID]; dup {
			if !bytes.Equal(prior, fs.Signature) {
				return 0, fmt.Errorf("%w: conflicting signatures from key %q", ErrFirmwareSignature, fs.KeyID)
			}
			return 0, fmt.Errorf("%w: duplicate signature from key %q", ErrFirmwareSignature, fs.KeyID)
		}
		seenSig[fs.KeyID] = append([]byte(nil), fs.Signature...)
		distinctKeys[string(entry.PubKey)] = struct{}{}
	}

	return len(distinctKeys), nil
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
