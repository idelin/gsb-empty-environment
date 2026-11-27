package treekem

import (
	"crypto/ecdh"
	"errors"
	"testing"
)

func TestGhostParentInjectionDetected(t *testing.T) {
	mesh := newTestMesh(t)
	mesh.add("bob")
	mesh.add("carol")
	mesh.add("dave")
	validTree := mesh.hosts["alice"].Tree

	var ghostIndex Index = -1
	for i, node := range validTree.Nodes {
		if Index(i)%2 == 1 && node.Blank() {
			ghostIndex = Index(i)
			break
		}
	}
	if ghostIndex < 0 {
		t.Fatal("expected a blank parent slot for ghost injection setup")
	}

	ghostTree := validTree.Clone()
	ghostPublicKey, err := randomSecret()
	if err != nil {
		t.Fatal(err)
	}
	ghostTree.Nodes[ghostIndex] = &TreeNode{
		Index: ghostIndex,
		Parent: &ParentNode{
			EncryptionKey: ghostPublicKey,
			ParentHash:    []byte("attacker-controlled-upward-hash"),
		},
	}
	err = VerifyParentHashes(ghostTree)
	if !errors.Is(err, ErrGhostParent) {
		t.Fatalf("VerifyParentHashes(ghost)=%v, want ErrGhostParent", err)
	}

	oversized := validTree.Clone()
	oversized.Nodes = append(oversized.Nodes, &TreeNode{Index: Index(len(oversized.Nodes))})
	if err := VerifyParentHashes(oversized); err == nil {
		t.Fatal("out-of-compact-tree node injection was accepted")
	}

	outOfRange := Index(validTree.Width() + 99)
	if _, err := validTree.Resolution(outOfRange); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("Resolution(out-of-range)=%v, want ErrInvalidIndex", err)
	}
}

func TestTamperedParentHashDetected(t *testing.T) {
	mesh := newTestMesh(t)
	mesh.add("bob")
	tree := mesh.hosts["alice"].Tree

	if _, err := VerifyParentHashChain(tree, 0); err != nil {
		t.Fatalf("honest parent hash chain: %v", err)
	}

	tamperedParent := tree.Clone()
	root := tamperedParent.Root()
	if tamperedParent.Nodes[root] == nil || tamperedParent.Nodes[root].Parent == nil {
		t.Fatal("test setup: root parent missing")
	}
	tamperedParent.Nodes[root].Parent.ParentHash = []byte("forged-upward-parent-hash")
	if _, err := VerifyParentHashChain(tamperedParent, 0); !errors.Is(err, ErrParentHashMismatch) {
		t.Fatalf("tampered parent chain error=%v, want ErrParentHashMismatch", err)
	}
	if err := VerifyParentHashes(tamperedParent); !errors.Is(err, ErrGhostParent) {
		t.Fatalf("whole-tree tampered parent error=%v, want ErrGhostParent", err)
	}

	tamperedLeaf := tree.Clone()
	leafHash := tamperedLeaf.Nodes[0].Leaf.ParentHash
	if len(leafHash) == 0 {
		t.Fatal("expected Alice leaf parent hash")
	}
	leafHash[0] ^= 0xff
	if _, err := VerifyParentHashChain(tamperedLeaf, 0); !errors.Is(err, ErrParentHashMismatch) {
		t.Fatalf("tampered leaf hash chain error=%v, want ErrParentHashMismatch", err)
	}
	if err := VerifyParentHashes(tamperedLeaf); err == nil {
		t.Fatal("tampered leaf parent_hash was accepted by whole-tree verification")
	}
}

func TestMaliciousBlankedNodeIsAnchoredAndChainRejected(t *testing.T) {
	mesh := newTestMesh(t)
	mesh.add("bob")
	validTree := mesh.hosts["alice"].Tree
	trustedHash := validTree.Hash()

	blanked := validTree.Clone()
	root := blanked.Root()
	blanked.Nodes[root] = &TreeNode{Index: root}

	if _, err := VerifyParentHashChain(blanked, 0); !errors.Is(err, ErrGhostParent) {
		t.Fatalf("blanked current parent chain error=%v, want ErrGhostParent", err)
	}
	if err := VerifyCommittedTree(blanked, 0); err == nil {
		t.Fatal("VerifyCommittedTree accepted blanked current root")
	}
	if err := VerifyTree(blanked, trustedHash); !errors.Is(err, ErrTreeHashMismatch) {
		t.Fatalf("VerifyTree blanking error=%v, want ErrTreeHashMismatch", err)
	}
}

func TestRemovedMemberCannotRetainPathSecret(t *testing.T) {
	mesh := newTestMesh(t)
	bob := mesh.add("bob")
	mesh.add("carol")

	bobPosition := bob.LeafPosition
	retainedIdentityKey := bob.Identity.encPriv
	retainedRing := map[Index]*ecdh.PrivateKey{}
	for index, key := range bob.Ring {
		retainedRing[index] = key
	}

	// Carol removes Bob. Her copath at the root resolves through the blanked
	// Bob/Alice parent directly to Alice's leaf; Bob's old parent key is not a
	// recipient key.
	commit := mesh.commitAs("carol", func(h *Host) {
		h.QueueRemove(bobPosition)
	})

	if commit.IncludesRecipientLeaf(bobPosition) {
		t.Fatalf("removed leaf %d is explicitly in recipient leaf set", bobPosition)
	}
	for _, recipientLeaf := range commit.AllRecipientLeaves() {
		if recipientLeaf == bobPosition {
			t.Fatal("removed Bob appeared in flattened recipients")
		}
	}

	_, exists := mesh.hosts["bob"]
	if exists {
		t.Fatal("test mesh should have removed Bob from active hosts")
	}
	// The pointer remains available to simulate a compromised/removed member.
	if _, err := bob.AcceptCommit(commit); !errors.Is(err, ErrRemovedFromGroup) {
		t.Fatalf("Bob AcceptCommit error=%v, want ErrRemovedFromGroup", err)
	}

	for stepIndex, step := range commit.Path.Steps {
		for recipientIndex, recipient := range step.Recipients {
			aad := envelopeAAD(commit.GroupID, commit.Epoch, commit.Committer, step.Node, recipient.Node)
			candidateKeys := []*ecdh.PrivateKey{retainedIdentityKey}
			if key := retainedRing[recipient.Node]; key != nil {
				candidateKeys = append(candidateKeys, key)
			}
			for _, key := range candidateKeys {
				if _, err := openEnvelope(key, aad, recipient.Envelope); err == nil {
					t.Fatalf("removed Bob decrypted step %d recipient %d/%d node %d",
						stepIndex, recipientIndex, recipient.Node, recipient.Node)
				}
			}
		}
	}

	// A malicious DS cannot add an envelope addressed to Bob without changing
	// the exact canonical resolution.
	attackCommit := shallowCloneCommitWithBobEnvelope(t, commit, bobPosition, bob.Identity.Public().EncryptionKey)
	alice := mesh.hosts["alice"]
	if err := ValidatePathRecipients(alice.Tree, attackCommit); err == nil {
		t.Fatal("malicious extra recipient for removed Bob was accepted")
	}

	// Remaining members already accepted in commitAs and share the new epoch.
	if alice.Epoch != commit.Epoch {
		t.Fatalf("Alice epoch=%d, commit=%d", alice.Epoch, commit.Epoch)
	}
	if bytesEqual(alice.Secrets.EpochSecret, bob.Secrets.EpochSecret) {
		t.Fatal("removed Bob retained the new epoch secret")
	}
}

type retainedECDHKey = ecdh.PrivateKey

func shallowCloneCommitWithBobEnvelope(t *testing.T, commit *Commit, bobPosition int, bobPublicKey []byte) *Commit {
	t.Helper()
	attack := *commit
	pathCopy := *commit.Path
	steps := append([]PathStep(nil), commit.Path.Steps...)
	extraStepIndex := 0
	extra := Recipient{
		Node:            Leaf(bobPosition),
		RecipientKey:    append([]byte(nil), bobPublicKey...),
		RecipientLeaves: []int{bobPosition},
	}
	envelope, err := sealEnvelope(bobPublicKey, []byte("rogue-aad"), []byte("rogue-path-secret"))
	if err != nil {
		t.Fatal(err)
	}
	extra.Envelope = envelope
	steps[extraStepIndex].Recipients = append(append([]Recipient(nil), steps[extraStepIndex].Recipients...), extra)
	pathCopy.Steps = steps
	attack.Path = &pathCopy
	return &attack
}
