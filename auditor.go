package kt

import (
	"crypto/ed25519"
)

// AuditorView is one independent auditor's signed receipt for a single epoch.
// Two auditors must observe identical tree roots and identical migration
// history for a given epoch number.
type AuditorView struct {
	AuditorID  string
	Signed     *SignedRoot
	Migrations *MigrationLog
}

// ViewFingerprint is the comparable digest of an auditor view.
type ViewFingerprint struct {
	Epoch         uint32
	TreeRoot      Hash
	MigrationHash Hash
}

// Fingerprint returns the comparable digest.
func (v *AuditorView) Fingerprint() ViewFingerprint {
	fp := ViewFingerprint{}
	if v.Signed != nil {
		fp.Epoch = v.Signed.Body.Epoch
		fp.TreeRoot = v.Signed.Body.TreeRoot
		fp.MigrationHash = v.Signed.Body.MigrationHash
	}
	return fp
}

// ForkProof is an independently verifiable fraud proof demonstrating that two
// auditors received conflicting views for the same epoch. Any third party can
// verify the two signed receipts against the directory key and observe the
// conflict without trusting the prover.
type ForkProof struct {
	Epoch      uint32
	Reason     string
	ViewA      AuditorView
	ViewB      AuditorView
	RootHash   Hash
	ForkDigest Hash
}

// CompareAuditorViews checks two auditor receipts for the same epoch. It
// verifies both signatures against trustedKey and returns a ForkProof when
// either the tree root or the migration-history commitment differs.
func CompareAuditorViews(a, b *AuditorView, trustedKey ed25519.PublicKey) (*ForkProof, error) {
	if a == nil || b == nil || a.Signed == nil || b.Signed == nil {
		return nil, ErrNilEntry
	}
	if err := a.Signed.VerifySignature(trustedKey); err != nil {
		return nil, err
	}
	if err := b.Signed.VerifySignature(trustedKey); err != nil {
		return nil, err
	}
	if a.Signed.Body.Epoch != b.Signed.Body.Epoch {
		return nil, ErrEpochSequence
	}
	// Cross-check that each carried migration log really hashes to its signed
	// commitment, so a lying auditor cannot hide a migration-history conflict.
	if a.Migrations != nil {
		if a.Migrations.Epoch != a.Signed.Body.Epoch || a.Migrations.Hash() != a.Signed.Body.MigrationHash {
			return nil, ErrMigrationHash
		}
	}
	if b.Migrations != nil {
		if b.Migrations.Epoch != b.Signed.Body.Epoch || b.Migrations.Hash() != b.Signed.Body.MigrationHash {
			return nil, ErrMigrationHash
		}
	}
	fa, fb := a.Fingerprint(), b.Fingerprint()
	if fa == fb {
		return nil, nil
	}
	reason := "tree-root fork"
	if fa.TreeRoot == fb.TreeRoot {
		reason = "migration-history fork"
	}
	fp := &ForkProof{
		Epoch:    fa.Epoch,
		Reason:   reason,
		ViewA:    *a,
		ViewB:    *b,
		RootHash: fa.TreeRoot,
	}
	fp.ForkDigest = taggedHash(domainForkProof,
		u32Bytes(fp.Epoch),
		[]byte(reason),
		fa.TreeRoot[:],
		fa.MigrationHash[:],
		fb.TreeRoot[:],
		fb.MigrationHash[:])
	return fp, nil
}

// VerifyForkProof independently re-checks a fork proof: both receipts must
// validate against trustedKey, target the same epoch, and genuinely conflict.
func (f *ForkProof) Verify(trustedKey ed25519.PublicKey) error {
	if f == nil {
		return ErrNilEntry
	}
	if err := f.ViewA.Signed.VerifySignature(trustedKey); err != nil {
		return err
	}
	if err := f.ViewB.Signed.VerifySignature(trustedKey); err != nil {
		return err
	}
	ea := f.ViewA.Signed.Body.Epoch
	eb := f.ViewB.Signed.Body.Epoch
	if ea != eb || ea != f.Epoch {
		return ErrEpochSequence
	}
	fa, fb := f.ViewA.Fingerprint(), f.ViewB.Fingerprint()
	if fa == fb {
		return ErrNotAFork
	}
	want := taggedHash(domainForkProof,
		u32Bytes(f.Epoch),
		[]byte(f.Reason),
		fa.TreeRoot[:],
		fa.MigrationHash[:],
		fb.TreeRoot[:],
		fb.MigrationHash[:])
	if want != f.ForkDigest {
		return ErrForkDigest
	}
	return nil
}

var (
	// ErrNotAFork indicates two views that actually agree.
	ErrNotAFork = prefixError("kt: auditor views do not conflict")
	// ErrForkDigest indicates a tampered fork-proof digest.
	ErrForkDigest = prefixError("kt: fork proof digest mismatch")
)
