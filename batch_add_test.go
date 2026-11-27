package treekem

import (
	"bytes"
	"testing"
)

func TestBatchAddTwoMembersInOneCommit(t *testing.T) {
	alice, err := NewGroup([]byte("batch"), "alice")
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := NewIdentity("bob")
	if err != nil {
		t.Fatal(err)
	}
	carolID, err := NewIdentity("carol")
	if err != nil {
		t.Fatal(err)
	}
	bobKP, err := bobID.KeyPackage()
	if err != nil {
		t.Fatal(err)
	}
	carolKP, err := carolID.KeyPackage()
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.QueueAdd(bobKP); err != nil {
		t.Fatal(err)
	}
	if err := alice.QueueAdd(carolKP); err != nil {
		t.Fatal(err)
	}
	commit, welcomes, err := alice.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if len(welcomes) != 2 {
		t.Fatalf("welcomes=%d, want 2", len(welcomes))
	}

	bob, err := JoinFromWelcome(bobID, welcomes[0], commit)
	if err != nil {
		t.Fatal(err)
	}
	carol, err := JoinFromWelcome(carolID, welcomes[1], commit)
	if err != nil {
		t.Fatal(err)
	}
	if bob.LeafPosition == carol.LeafPosition {
		t.Fatal("batch Add assigned same leaf position")
	}
	for name, host := range map[string]*Host{"bob": bob, "carol": carol} {
		if !bytes.Equal(host.Secrets.EpochSecret, alice.Secrets.EpochSecret) {
			t.Fatalf("%s epoch secret mismatch", name)
		}
		if !host.Tree.Equal(alice.Tree) {
			t.Fatalf("%s tree mismatch", name)
		}
	}
}
