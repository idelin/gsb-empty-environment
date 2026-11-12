package netsec

import "fmt"

// Attestation is the signed window root. It records the manifest it was built
// from, every partition's terminal prefix digest and the aggregate root that
// summarizes them. A non-nil Signature is verifiable with the Verifier named
// by KeyID.
type Attestation struct {
	Window         WindowID        `json:"window"`
	ManifestDigest Digest          `json:"manifest_digest"`
	PrevRoot       Digest          `json:"prev_root"`
	Roots          []PartitionRoot `json:"roots"`
	Root           Digest          `json:"root"`
	KeyID          string          `json:"key_id,omitempty"`
	Signature      []byte          `json:"signature,omitempty"`
}

// Verify checks that the attestation is internally consistent and, when
// signed, that its signature authenticates the canonical root message.
//
// It recomputes the aggregate root from the listed per-partition roots and
// compares it against Root. manifest optionally pins the exact manifest:
// pass nil to skip the manifest cross-check.
func (a *Attestation) Verify(manifest *Manifest, verifier Verifier) error {
	if len(a.Roots) == 0 {
		return fmt.Errorf("%w: attestation has no partition roots", ErrSignature)
	}

	// Roots must be in the canonical sorted order used at sealing time.
	for i := 1; i < len(a.Roots); i++ {
		if a.Roots[i-1].Partition >= a.Roots[i].Partition {
			return fmt.Errorf("%w: partition roots are not canonically ordered",
				ErrSignature)
		}
	}

	gotRoot := AggregateDigest(a.Window, a.ManifestDigest, a.PrevRoot, a.Roots)
	if gotRoot != a.Root {
		return fmt.Errorf("%w: aggregate root mismatch", ErrSignature)
	}

	if manifest != nil {
		wantDigest, err := manifest.Digest()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSignature, err)
		}
		if wantDigest != a.ManifestDigest {
			return fmt.Errorf("%w: manifest digest mismatch", ErrSignature)
		}
		if manifest.Window != a.Window || manifest.PrevRoot != a.PrevRoot {
			return fmt.Errorf("%w: manifest window or previous-root mismatch",
				ErrSignature)
		}
		if len(manifest.Partitions) != len(a.Roots) {
			return fmt.Errorf("%w: manifest partition count mismatch", ErrSignature)
		}
		compiled, err := manifest.compile()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSignature, err)
		}
		for idx, id := range compiled.order {
			if a.Roots[idx].Partition != id {
				return fmt.Errorf("%w: partition set mismatch", ErrSignature)
			}
		}
	}

	if len(a.Signature) == 0 {
		if verifier != nil {
			return fmt.Errorf("%w: attestation is unsigned", ErrSignature)
		}
		return nil
	}
	if verifier == nil {
		return fmt.Errorf("%w: verifier required for signed attestation", ErrSignature)
	}
	if verifier.KeyID() != a.KeyID {
		return fmt.Errorf("%w: key id %q, attestation key %q",
			ErrSignature, verifier.KeyID(), a.KeyID)
	}
	msg := rootMessage(a.Window, a.ManifestDigest, a.PrevRoot, a.Roots)
	if !verifier.Verify(msg, a.Signature) {
		return ErrSignature
	}
	return nil
}

// Signed reports whether the attestation carries a signature.
func (a *Attestation) Signed() bool {
	return len(a.Signature) > 0
}

// RootFor returns the terminal prefix digest recorded for one partition.
func (a *Attestation) RootFor(partition PartitionID) (Digest, bool) {
	for _, root := range a.Roots {
		if root.Partition == partition {
			return root.LastDigest, true
		}
	}
	return Digest{}, false
}
