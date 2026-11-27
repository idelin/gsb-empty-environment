package treekem

import (
	"crypto/ed25519"
	"errors"
	"fmt"
)

func encodeKeyPackage(hb *hashBuilder, keyPackage *KeyPackage) {
	if keyPackage == nil {
		hb.byte(0)
		return
	}
	hb.byte(1)
	hb.bytes([]byte(keyPackage.Public.ID))
	hb.bytes(keyPackage.Public.EncryptionKey)
	hb.bytes(keyPackage.Public.SignatureKey)
	hb.bytes(keyPackage.Signature)
}

func encodeEnvelope(hb *hashBuilder, envelope *Envelope) {
	if envelope == nil {
		hb.byte(0)
		return
	}
	hb.byte(1)
	hb.bytes(envelope.EphemeralPublicKey)
	hb.bytes(envelope.Nonce)
	hb.bytes(envelope.Ciphertext)
}

func encodeProposal(hb *hashBuilder, proposal Proposal) {
	hb.byte(byte(len(proposal.Kind))) // not unambiguous but kind string follows
	hb.bytes([]byte(proposal.Kind))
	switch proposal.Kind {
	case ProposalAdd:
		encodeKeyPackage(hb, proposal.Add)
	case ProposalRemove:
		hb.uint32(uint32(proposal.RemoveLeafPosition))
	case ProposalUpdate:
		if proposal.Update == nil {
			hb.byte(0)
		} else {
			hb.uint32(uint32(proposal.Update.LeafPosition))
			encodeKeyPackage(hb, proposal.Update.KeyPackage)
		}
	}
}

func encodeCommit(commit *Commit) []byte {
	hb := &hashBuilder{}
	hb.buf = append(hb.buf, []byte("treekem-signed-commit-v1")...)
	hb.bytes(commit.GroupID)
	hb.uint64(commit.Epoch)
	hb.uint32(uint32(commit.Committer))
	hb.uint16(uint16(len(commit.Proposals)))
	for _, proposal := range commit.Proposals {
		encodeProposal(hb, proposal)
	}
	if commit.Path == nil {
		hb.byte(0)
	} else {
		hb.byte(1)
		path := commit.Path
		hb.uint32(uint32(path.Committer))
		hb.bytes(path.LeafEncryptionKey)
		hb.bytes(path.LeafSignatureKey)
		hb.bytes(path.LeafParentHash)
		hb.bytes(path.LeafSignature)
		hb.uint16(uint16(len(path.Steps)))
		for _, step := range path.Steps {
			hb.uint32(uint32(step.Node))
			hb.bytes(step.EncryptionKey)
			hb.bytes(step.ParentHash)
			hb.indexes(step.OriginalChildResolution)
			hb.uint16(uint16(len(step.Recipients)))
			for _, recipient := range step.Recipients {
				hb.uint32(uint32(recipient.Node))
				hb.bytes(recipient.RecipientKey)
				hb.positions(recipient.RecipientLeaves)
				encodeEnvelope(hb, recipient.Envelope)
			}
		}
	}
	hb.bytes(commit.Confirmation)
	return hb.buf
}

// CommitSigningBytes returns the deterministic bytes covered by the committer
// signature and confirmed transcript.
func CommitSigningBytes(commit *Commit) []byte { return encodeCommit(commit) }

func signCommit(identity *Identity, commit *Commit) error {
	if identity == nil {
		return errors.New("treekem: missing committer identity")
	}
	commit.Signature = identity.Sign(CommitSigningBytes(commit))
	return nil
}

// VerifyCommitSignature checks the committer's signature against the pre-commit
// leaf signature key.
func VerifyCommitSignature(treeBefore *RatchetTree, commit *Commit) error {
	if commit == nil {
		return errors.New("treekem: nil commit")
	}
	leafIndex := Leaf(commit.Committer)
	raw, err := treeBefore.node(leafIndex)
	if err != nil {
		return err
	}
	if raw.Blank() || raw.Leaf == nil {
		return fmt.Errorf("treekem: committer leaf %d is not active", commit.Committer)
	}
	if len(raw.Leaf.SignatureKey) != ed25519.PublicKeySize {
		return errors.New("treekem: committer has invalid signature key")
	}
	if !verifySignature(ed25519.PublicKey(raw.Leaf.SignatureKey), CommitSigningBytes(commit), commit.Signature) {
		return errors.New("treekem: invalid commit signature")
	}
	return nil
}
