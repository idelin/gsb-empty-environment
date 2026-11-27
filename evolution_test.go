package treekem

import (
	"bytes"
	"errors"
	"testing"
)

type testMesh struct {
	t       *testing.T
	groupID []byte
	ids     map[string]*Identity
	hosts   map[string]*Host
	order   []string
}

func newTestMesh(t *testing.T) *testMesh {
	t.Helper()
	groupID := []byte("group-evolution")
	alice, err := NewGroup(groupID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return &testMesh{
		t:       t,
		groupID: groupID,
		ids:     map[string]*Identity{"alice": alice.Identity},
		hosts:   map[string]*Host{"alice": alice},
		order:   []string{"alice"},
	}
}

func (m *testMesh) add(name string) *Host {
	m.t.Helper()
	if _, exists := m.ids[name]; exists {
		m.t.Fatalf("member %s already exists", name)
	}
	identity, err := NewIdentity(name)
	if err != nil {
		m.t.Fatal(err)
	}
	keyPackage, err := identity.KeyPackage()
	if err != nil {
		m.t.Fatal(err)
	}
	committer := m.hosts["alice"]
	if err := committer.QueueAdd(keyPackage); err != nil {
		m.t.Fatal(err)
	}
	commit, welcomes, err := committer.Commit()
	if err != nil {
		m.t.Fatalf("add %s commit: %v", name, err)
	}
	if len(welcomes) != 1 {
		m.t.Fatalf("welcomes=%d", len(welcomes))
	}

	for existingName, host := range m.hosts {
		if existingName == "alice" {
			continue
		}
		if _, err := host.AcceptCommit(commit); err != nil {
			m.t.Fatalf("existing member %s accept add-%s: %v", existingName, name, err)
		}
	}
	newHost, err := JoinFromWelcome(identity, welcomes[0], commit)
	if err != nil {
		m.t.Fatalf("join %s: %v", name, err)
	}
	m.ids[name] = identity
	m.hosts[name] = newHost
	m.order = append(m.order, name)
	m.assertConsistent(commit.Epoch)
	return newHost
}

func (m *testMesh) commitAs(name string, prepare func(*Host)) *Commit {
	m.t.Helper()
	committer := m.hosts[name]
	if committer == nil {
		m.t.Fatalf("unknown committer %s", name)
	}
	if prepare != nil {
		prepare(committer)
	}
	commit, _, err := committer.Commit()
	if err != nil {
		m.t.Fatalf("%s commit: %v", name, err)
	}
	for memberName, host := range m.hosts {
		if memberName == name {
			continue
		}
		if _, err := host.AcceptCommit(commit); err != nil {
			if errors.Is(err, ErrRemovedFromGroup) {
				delete(m.hosts, memberName)
				continue
			}
			m.t.Fatalf("%s accept commit by %s: %v", memberName, name, err)
		}
	}
	m.assertConsistent(commit.Epoch)
	return commit
}

func (m *testMesh) assertConsistent(epoch uint64) {
	m.t.Helper()
	var reference *Host
	referenceName := ""
	for _, name := range m.order {
		host := m.hosts[name]
		if host == nil {
			continue
		}
		if reference == nil {
			reference = host
			referenceName = name
			continue
		}
		if host.Epoch != epoch || reference.Epoch != epoch {
			m.t.Fatalf("epoch mismatch at %s: %d/%d want %d", name, host.Epoch, reference.Epoch, epoch)
		}
		if !reference.Tree.Equal(host.Tree) {
			m.t.Fatalf("tree mismatch between %s and %s at epoch %d", referenceName, name, epoch)
		}
		if !bytes.Equal(reference.Secrets.EpochSecret, host.Secrets.EpochSecret) {
			m.t.Fatalf("epoch secret mismatch between %s and %s", referenceName, name)
		}
		if !bytes.Equal(reference.InitSecret, host.InitSecret) {
			m.t.Fatalf("init secret mismatch between %s and %s", referenceName, name)
		}
		if err := VerifyParentHashes(host.Tree); err != nil {
			m.t.Fatalf("parent hash verification for %s: %v", name, err)
		}
	}
}

func TestMultiEpochLegalEvolution(t *testing.T) {
	mesh := newTestMesh(t)
	mesh.add("bob")
	mesh.add("carol")
	mesh.add("dave")

	mesh.commitAs("bob", func(h *Host) {
		if _, err := h.RotateIdentityAndQueueUpdate(); err != nil {
			t.Fatal(err)
		}
	})

	mesh.commitAs("carol", func(h *Host) {
		h.QueueRemove(mesh.hosts["dave"].LeafPosition)
	})

	mesh.add("eve")

	mesh.commitAs("bob", func(h *Host) {
		if _, err := h.RotateIdentityAndQueueUpdate(); err != nil {
			t.Fatal(err)
		}
	})
	mesh.commitAs("eve", func(h *Host) {
		if _, err := h.RotateIdentityAndQueueUpdate(); err != nil {
			t.Fatal(err)
		}
	})

	if len(mesh.hosts) != 4 {
		t.Fatalf("active hosts=%d, want alice/bob/carol/eve", len(mesh.hosts))
	}
}
