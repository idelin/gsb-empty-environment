package treekem_test

import (
	"bytes"
	"testing"

	treekem "formal.local/w27-ae76a371f2"
)

func TestExportedHostAPI(t *testing.T) {
	alice, err := treekem.NewGroup([]byte("external"), "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := treekem.NewIdentity("bob")
	if err != nil {
		t.Fatal(err)
	}
	keyPackage, err := bob.KeyPackage()
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

	bobHost, err := treekem.JoinFromWelcome(bob, welcomes[0], commit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(alice.Secrets.EpochSecret, bobHost.Secrets.EpochSecret) {
		t.Fatal("epoch secrets differ through exported API")
	}
	if _, err := treekem.DirectPath(treekem.Leaf(0), 2); err != nil {
		t.Fatal(err)
	}
	resolution, err := alice.Tree.Resolution(alice.Tree.Root())
	if err != nil || len(resolution) == 0 {
		t.Fatalf("root resolution=%v err=%v", resolution, err)
	}
}
