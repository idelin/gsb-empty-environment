package userns_test

import (
	"errors"
	"io/fs"
	"testing"

	"formal.local/w27-6467acc7b4/userns"
)

func TestIDMappingRoundTrip(t *testing.T) {
	m := userns.IDMap{Entries: []userns.IDMapEntry{
		{Container: 0, Host: 100000, Count: 100},
		{Container: 1000, Host: 200000, Count: 10},
	}}
	host, err := m.ToHost(0)
	if err != nil || host != 100000 {
		t.Fatalf("ToHost(0) = %d, %v", host, err)
	}
	host, err = m.ToHost(1005)
	if err != nil || host != 200005 {
		t.Fatalf("ToHost(1005) = %d, %v", host, err)
	}
	if _, err := m.ToHost(500); !errors.Is(err, userns.ErrNoMapping) {
		t.Fatalf("expected ErrNoMapping, got %v", err)
	}
	container, err := m.ToContainer(100099)
	if err != nil || container != 99 {
		t.Fatalf("ToContainer(100099) = %d, %v", container, err)
	}
}

func TestNestedNamespaceHostID(t *testing.T) {
	root := userns.NewInitial()
	level1 := root.Child("l1",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 10}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 10}}},
		"CAP_SYS_ADMIN")
	level2 := level1.Child("l2",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 5, Count: 5}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 5, Count: 5}}})

	cred := &userns.Cred{NS: level2, UID: 0, GID: 0}
	hostUID, err := cred.HostUID()
	if err != nil {
		t.Fatal(err)
	}
	// 0 in l2 -> 5 in l1 -> 1005 on the host.
	if hostUID != 1005 {
		t.Fatalf("nested host uid = %d, want 1005", hostUID)
	}
	if level2.IsInitial() || !root.IsInitial() {
		t.Fatal("initial namespace detection wrong")
	}
	if level2.Depth() != 2 {
		t.Fatalf("depth = %d, want 2", level2.Depth())
	}
	if !level1.HasCap("CAP_SYS_ADMIN") || level2.HasCap("CAP_SYS_ADMIN") {
		t.Fatal("capability inheritance must be explicit")
	}
}

func TestDACCheck(t *testing.T) {
	root := userns.NewInitial()
	ns := root.Child("u",
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}},
		userns.IDMap{Entries: []userns.IDMapEntry{{Container: 0, Host: 1000, Count: 1}}})

	owner := &userns.Cred{NS: ns, UID: 0, GID: 0}
	other := &userns.Cred{NS: root, UID: 2000, GID: 2000}

	// File owned by host uid 1000, mode 0600.
	var mode fs.FileMode = 0o600
	if !owner.CanRead(1000, 1000, mode) {
		t.Fatal("owner should read")
	}
	if other.CanRead(1000, 1000, mode) {
		t.Fatal("unrelated uid must not read 0600 file")
	}

	// World-readable file allows everyone.
	if !other.CanRead(1000, 1000, 0o644) {
		t.Fatal("0644 file must be world readable")
	}

	// Group access via supplementary groups.
	groupCred := &userns.Cred{NS: root, UID: 2000, GID: 2000, Groups: []uint32{1000}}
	if !groupCred.CanRead(1000, 1000, 0o640) {
		t.Fatal("supplementary group should grant read")
	}
}
