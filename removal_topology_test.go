package treekem

import (
	"crypto/ecdh"
	"errors"
	"fmt"
	"testing"
)

func assertHostCannotDecryptCommit(t *testing.T, host *Host, commit *Commit) {
	t.Helper()
	for stepIndex, step := range commit.Path.Steps {
		for recipientIndex, recipient := range step.Recipients {
			aad := envelopeAAD(commit.GroupID, commit.Epoch, commit.Committer, step.Node, recipient.Node)
			keys := []*ecdh.PrivateKey{host.Identity.encPriv}
			for _, key := range host.Ring {
				keys = append(keys, key)
			}
			for _, key := range keys {
				if key == nil {
					continue
				}
				if _, err := openEnvelope(key, aad, recipient.Envelope); err == nil {
					t.Fatalf(
						"removed leaf %d decrypted step %d recipient %d node %d",
						host.LeafPosition, stepIndex, recipientIndex, recipient.Node,
					)
				}
			}
		}
	}
}

func TestRemovalAcrossTreePositions(t *testing.T) {
	names := []string{"alice", "bob", "carol", "dave"}
	for targetPosition := 0; targetPosition < len(names); targetPosition++ {
		t.Run(fmt.Sprintf("remove-%s", names[targetPosition]), func(t *testing.T) {
			mesh := newTestMesh(t)
			for _, name := range names[1:] {
				mesh.add(name)
			}
			target := mesh.hosts[names[targetPosition]]
			committer := "alice"
			if targetPosition == 0 {
				committer = "bob"
			}

			commit := mesh.commitAs(committer, func(h *Host) {
				h.QueueRemove(target.LeafPosition)
			})
			if commit.IncludesRecipientLeaf(target.LeafPosition) {
				t.Fatalf("removed leaf %d appears in recipients", target.LeafPosition)
			}
			if _, err := target.AcceptCommit(commit); !errors.Is(err, ErrRemovedFromGroup) {
				t.Fatalf("target accept error=%v, want ErrRemovedFromGroup", err)
			}
			assertHostCannotDecryptCommit(t, target, commit)
		})
	}
}
