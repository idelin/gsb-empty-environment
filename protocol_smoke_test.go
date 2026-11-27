package treekem

import (
	"bytes"
	"testing"
)

func TestAddFirstMemberEndToEnd(t *testing.T) {
	groupID := []byte("group-smoke")
	alice, err := NewGroup(groupID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bobIdentity, err := NewIdentity("bob")
	if err != nil {
		t.Fatal(err)
	}
	keyPackage, err := bobIdentity.KeyPackage()
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.QueueAdd(keyPackage); err != nil {
		t.Fatal(err)
	}

	commit, welcomes, err := alice.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if len(welcomes) != 1 {
		t.Fatalf("welcomes=%d", len(welcomes))
	}
	bob, err := JoinFromWelcome(bobIdentity, welcomes[0], commit)
	if err != nil {
		t.Fatal(err)
	}
	if bob.Epoch != 1 || alice.Epoch != 1 {
		t.Fatalf("epochs alice=%d bob=%d", alice.Epoch, bob.Epoch)
	}
	if !bytes.Equal(alice.Secrets.EpochSecret, bob.Secrets.EpochSecret) {
		t.Fatalf("epoch secrets differ:\nalice=%x\nbob=%x", alice.Secrets.EpochSecret, bob.Secrets.EpochSecret)
	}
	if !alice.Tree.Equal(bob.Tree) {
		t.Fatal("trees differ after Add")
	}
	if err := VerifyParentHashes(bob.Tree); err != nil {
		t.Fatal(err)
	}
	if !commit.IncludesRecipientLeaf(1) {
		t.Fatal("Bob should be an encrypted recipient")
	}
}
