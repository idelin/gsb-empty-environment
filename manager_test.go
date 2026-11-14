package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestManager(t *testing.T) (*Manager, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	kr, err := NewKeyRing("k1", testKey(1), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(ManagerConfig{
		ServerInstance: "nfs-instance-1",
		KeyRing:        kr,
		Now:            clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, clock
}

func newExport(t *testing.T, m *Manager, tenant string, fsid uint64) ExportID {
	t.Helper()
	id, err := NewExportID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateExport(ExportSpec{ID: id, Tenant: tenant, Fsid: fsid}); err != nil {
		t.Fatal(err)
	}
	return id
}

func allPolicy(tenant string) Policy {
	return Policy{Tenant: tenant, Version: 1, Allowed: OpAll}
}

func mustObject(t *testing.T, m *Manager, spec ObjectSpec) ObjectInfo {
	t.Helper()
	info, err := m.CreateObject(spec)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func mustCap(t *testing.T, m *Manager, handle []byte) *Capability {
	t.Helper()
	cap, err := m.ParseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	return cap
}

func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestExportReplacementSameFsidInodeIsStale(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 100)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "obj-old",
		Fsid:     100,
		Inode:    42,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("old tenant data"),
	})
	oldHandle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	oldCap := mustCap(t, m, oldHandle)
	if err := m.Check(oldCap, OpRead); err != nil {
		t.Fatal(err)
	}

	if _, err := m.ReplaceExport(exportID, ExportSpec{Tenant: "tenant-b", Fsid: 100}); err != nil {
		t.Fatal(err)
	}
	newObj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "obj-new",
		Fsid:     100,
		Inode:    42,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-b"),
		Data:     []byte("new tenant data"),
	})
	if newObj.StableID == obj.StableID {
		t.Fatal("test setup reused stable ID unexpectedly")
	}
	requireError(t, m.Check(oldCap, OpRead), ErrStaleExport)

	newHandle, err := m.IssueHandle(exportID, newObj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	newCap := mustCap(t, m, newHandle)
	lease, err := m.Acquire(newCap, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	data, err := lease.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("new tenant data")) {
		t.Fatalf("new handle read %q", data)
	}
}

func TestDeleteExportRecreateSameUUIDAndLocationIsStale(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 200)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "obj-a",
		Inode:    9,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("a"),
	})
	handle, err := m.IssueHandleAt(exportID, 200, 9)
	if err != nil {
		t.Fatal(err)
	}
	cap := mustCap(t, m, handle)

	if err := m.DeleteExport(exportID); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(cap, OpRead), ErrStaleExport)
	if _, err := m.CreateExport(ExportSpec{ID: exportID, Tenant: "tenant-b", Fsid: 200}); err != nil {
		t.Fatal(err)
	}
	// Reuse both the fsid/inode and the backend stable ID.
	rebuilt := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: obj.StableID,
		Inode:    9,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-b"),
		Data:     []byte("b"),
	})
	if rebuilt.Generation <= obj.Generation {
		t.Fatalf("generation did not advance: old=%d new=%d", obj.Generation, rebuilt.Generation)
	}
	requireError(t, m.Check(cap, OpRead), ErrStaleExport)
}

func TestObjectDeleteRecreateAndLateCallback(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 300)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "obj-1",
		Inode:    7,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("first"),
	})
	handle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	cap := mustCap(t, m, handle)
	lease, err := m.Acquire(cap, OpOpen)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteObject(exportID, obj.StableID); err != nil {
		t.Fatal(err)
	}
	rebuilt := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: obj.StableID,
		Inode:    7,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("second"),
	})
	if rebuilt.Generation == obj.Generation {
		t.Fatal("recreated object reused generation")
	}
	requireError(t, m.Check(cap, OpRead), ErrStaleObject)

	called := false
	err = lease.Commit(func(view *ObjectView) error {
		called = true
		view.Data = []byte("late callback")
		return nil
	})
	requireError(t, err, ErrStaleObject)
	if called {
		t.Fatal("late storage callback ran after object recreation")
	}

	newHandle, err := m.IssueHandle(exportID, rebuilt.StableID)
	if err != nil {
		t.Fatal(err)
	}
	newLease, err := m.Acquire(mustCap(t, m, newHandle), OpRead)
	if err != nil {
		t.Fatal(err)
	}
	data, err := newLease.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("second")) {
		t.Fatalf("reused object was modified by late callback: %q", data)
	}
}

func TestCrossTenantMoveDoesNotRebindOldHandle(t *testing.T) {
	m, _ := newTestManager(t)
	from := newExport(t, m, "tenant-a", 400)
	to := newExport(t, m, "tenant-b", 401)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: from,
		StableID: "movable",
		Inode:    1,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("tenant a secret"),
	})
	oldHandle, err := m.IssueHandle(from, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	oldCap := mustCap(t, m, oldHandle)

	if _, err := m.MoveObject(from, to, obj.StableID, Policy{Tenant: "tenant-b", Version: 2, Allowed: OpAll}); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(oldCap, OpRead), ErrStaleObject)
	if _, err := m.Lookup(from, 400, 1); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("old location still resolves: %v", err)
	}
	newHandle, err := m.IssueHandleAt(to, 400, 1)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(mustCap(t, m, newHandle), OpRead)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := lease.Read(); err != nil || !bytes.Equal(data, []byte("tenant a secret")) {
		t.Fatalf("new tenant handle: data=%q err=%v", data, err)
	}
	// Moving the object back must not revive the original handle.
	movedBack, err := m.MoveObject(to, from, obj.StableID, allPolicy("tenant-a"))
	if err != nil {
		t.Fatal(err)
	}
	if movedBack.Generation <= obj.Generation {
		t.Fatalf("move did not advance object generation: old=%d new=%d", obj.Generation, movedBack.Generation)
	}
	requireError(t, m.Check(oldCap, OpRead), ErrStaleObject)
}

func TestPolicyChangeAndOperationDenial(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 500)
	readOnly := Policy{Tenant: "tenant-a", Version: 1, Allowed: OpLookup | OpOpen | OpRead}
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "policy-obj",
		Inode:    2,
		Type:     ObjectTypeFile,
		Policy:   readOnly,
		Data:     []byte("read only"),
	})
	handle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	cap := mustCap(t, m, handle)
	if err := m.Check(cap, OpRead); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(cap, OpWrite), ErrOperationDenied)
	lease, err := m.Acquire(cap, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	requireError(t, lease.Commit(func(view *ObjectView) error {
		called = true
		view.Data = []byte("write bypass")
		return nil
	}), ErrOperationDenied)
	if called {
		t.Fatal("read-only lease invoked a write callback")
	}

	// Changing the policy changes the committed digest even if the new policy
	// still allows reads.  The old handle must not silently adopt it.
	if _, err := m.ChangeObjectPolicy(exportID, obj.StableID, allPolicy("tenant-a")); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(cap, OpRead), ErrStalePolicy)
}

func TestSnapshotRollbackInvalidatesOldGenerations(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 600)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "snap-obj",
		Inode:    5,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("v1"),
	})
	oldHandle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	oldCap := mustCap(t, m, oldHandle)
	lease, err := m.Acquire(oldCap, OpOpen)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := m.TakeSnapshot(exportID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Write([]byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := m.RollbackSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(oldCap, OpRead), ErrStaleSnapshot)
	called := false
	requireError(t, lease.Commit(func(view *ObjectView) error {
		called = true
		return nil
	}), ErrStaleSnapshot)
	if called {
		t.Fatal("pre-rollback callback ran after rollback")
	}

	newHandle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	newCap := mustCap(t, m, newHandle)
	newLease, err := m.Acquire(newCap, OpRead)
	if err != nil {
		t.Fatal(err)
	}
	data, err := newLease.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("v1")) {
		t.Fatalf("rollback data = %q", data)
	}
}

func TestKeyRotationGraceCutoffAndExpiry(t *testing.T) {
	m, clock := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 700)
	oldObj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "old-obj",
		Inode:    1,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("old"),
	})
	oldHandle, err := m.IssueHandle(exportID, oldObj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	oldCap := mustCap(t, m, oldHandle)

	if err := m.RotateKey("k2", testKey(2)); err != nil {
		t.Fatal(err)
	}
	if err := m.Check(oldCap, OpRead); err != nil {
		t.Fatalf("old key should verify old generation during grace: %v", err)
	}
	newHandle, err := m.IssueHandle(exportID, oldObj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.KeyRing().ActiveKeyID(); got != "k2" {
		t.Fatalf("active key = %q", got)
	}
	newObj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "new-obj",
		Inode:    2,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("new"),
	})
	forged, err := m.codec.SealWithKey("k1", Claims{
		ExportID:         exportID,
		ExportGeneration: mustExportInfo(t, m, exportID).Generation,
		ObjectID:         newObj.StableID,
		ObjectGeneration: newObj.Generation,
		ObjectType:       newObj.Type,
		PolicyDigest:     newObj.Policy.Digest(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ParseHandle(forged); !errors.Is(err, ErrOldKeyNewObject) {
		t.Fatalf("retired key authorized new object: %v", err)
	}

	clock.Advance(2 * time.Hour)
	requireError(t, m.Check(oldCap, OpRead), ErrKeyExpired)
	if _, err := m.ParseHandle(oldHandle); !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("expired key decode: %v", err)
	}
	newCap := mustCap(t, m, newHandle)
	if err := m.Check(newCap, OpRead); err != nil {
		t.Fatalf("active-key handle failed after grace expiry: %v", err)
	}
}

func mustExportInfo(t *testing.T, m *Manager, id ExportID) ExportInfo {
	t.Helper()
	info, err := m.ExportInfo(id)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestLegalHardLinksKeepStableHandle(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 800)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "linked",
		Fsid:     800,
		Inode:    10,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("shared inode data"),
	})
	handle, err := m.IssueHandleAt(exportID, 800, 10)
	if err != nil {
		t.Fatal(err)
	}
	cap := mustCap(t, m, handle)
	if _, err := m.AddHardLink(exportID, obj.StableID, 800, 11); err != nil {
		t.Fatal(err)
	}
	if err := m.Check(cap, OpRead); err != nil {
		t.Fatalf("hard link invalidated old handle: %v", err)
	}
	aliasHandle, err := m.IssueHandleAt(exportID, 800, 11)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Check(mustCap(t, m, aliasHandle), OpRead); err != nil {
		t.Fatal(err)
	}

	// Removing one name of a multi-link object does not change its generation.
	if err := m.DeleteObjectAt(exportID, 800, 11); err != nil {
		t.Fatal(err)
	}
	if err := m.Check(cap, OpRead); err != nil {
		t.Fatalf("removing one hard link invalidated the object: %v", err)
	}
	// Removing the final link tombstones the object.
	if err := m.DeleteObjectAt(exportID, 800, 10); err != nil {
		t.Fatal(err)
	}
	requireError(t, m.Check(cap, OpRead), ErrStaleObject)
}

func TestLookupHandleChecksExportGenerationAndPolicy(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 850)
	dir := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "dir",
		Fsid:     850,
		Inode:    1,
		Type:     ObjectTypeDirectory,
		Policy:   allPolicy("tenant-a"),
	})
	child := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "child",
		Fsid:     850,
		Inode:    2,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
		Data:     []byte("child"),
	})
	dirHandle, err := m.IssueHandle(exportID, dir.StableID)
	if err != nil {
		t.Fatal(err)
	}
	dirCap := mustCap(t, m, dirHandle)
	info, err := m.LookupHandle(dirCap, 850, 2)
	if err != nil {
		t.Fatal(err)
	}
	if info.StableID != child.StableID || info.Generation != child.Generation {
		t.Fatalf("lookup resolved %+v, want child %+v", info, child)
	}

	noLookup := Policy{Tenant: "tenant-a", Version: 1, Allowed: OpOpen | OpRead}
	dir2 := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "dir-no-lookup",
		Fsid:     850,
		Inode:    3,
		Type:     ObjectTypeDirectory,
		Policy:   noLookup,
	})
	dir2Handle, err := m.IssueHandle(exportID, dir2.StableID)
	if err != nil {
		t.Fatal(err)
	}
	requireError(t, func() error {
		_, err := m.LookupHandle(mustCap(t, m, dir2Handle), 850, 2)
		return err
	}(), ErrOperationDenied)

	if _, err := m.ReplaceExport(exportID, ExportSpec{Tenant: "tenant-a", Fsid: 850}); err != nil {
		t.Fatal(err)
	}
	requireError(t, func() error {
		_, err := m.LookupHandle(dirCap, 850, 2)
		return err
	}(), ErrStaleExport)
}

func TestObjectTypeMismatchIsStale(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 875)
	obj := mustObject(t, m, ObjectSpec{
		ExportID: exportID,
		StableID: "typed",
		Inode:    1,
		Type:     ObjectTypeFile,
		Policy:   allPolicy("tenant-a"),
	})
	handle, err := m.IssueHandle(exportID, obj.StableID)
	if err != nil {
		t.Fatal(err)
	}
	cap := mustCap(t, m, handle)
	m.mu.Lock()
	m.exports[exportID].objects[obj.StableID].objectType = ObjectTypeSymlink
	m.mu.Unlock()
	requireError(t, m.Check(cap, OpRead), ErrTypeMismatch)
}

func TestConcurrentDeleteAndOpenUsesGenerationSnapshot(t *testing.T) {
	m, _ := newTestManager(t)
	exportID := newExport(t, m, "tenant-a", 900)
	policy := allPolicy("tenant-a")
	const rounds = 64
	for i := 0; i < rounds; i++ {
		stable := StableObjectID("race-obj")
		obj := mustObject(t, m, ObjectSpec{
			ExportID: exportID,
			StableID: stable,
			Fsid:     900,
			Inode:    uint64(1000 + i),
			Type:     ObjectTypeFile,
			Policy:   policy,
			Data:     []byte("original"),
		})
		handle, err := m.IssueHandle(exportID, obj.StableID)
		if err != nil {
			t.Fatal(err)
		}
		cap := mustCap(t, m, handle)

		var wg sync.WaitGroup
		var lease *Lease
		var acquireErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			lease, acquireErr = m.Acquire(cap, OpOpen)
		}()
		go func() {
			defer wg.Done()
			_ = m.DeleteObject(exportID, stable)
		}()
		wg.Wait()

		if acquireErr != nil {
			if !errors.Is(acquireErr, ErrStale) {
				t.Fatalf("round %d: unexpected acquire error: %v", i, acquireErr)
			}
		}
		rebuilt := mustObject(t, m, ObjectSpec{
			ExportID: exportID,
			StableID: stable,
			Fsid:     900,
			Inode:    uint64(1000 + i),
			Type:     ObjectTypeFile,
			Policy:   policy,
			Data:     []byte("rebuilt"),
		})
		if lease != nil {
			called := false
			err := lease.Commit(func(view *ObjectView) error {
				called = true
				view.Data = []byte("late")
				return nil
			})
			if !errors.Is(err, ErrStale) {
				t.Fatalf("round %d: late commit err = %v", i, err)
			}
			if called {
				t.Fatalf("round %d: late callback ran", i)
			}
		}
		newHandle, err := m.IssueHandle(exportID, rebuilt.StableID)
		if err != nil {
			t.Fatal(err)
		}
		newLease, err := m.Acquire(mustCap(t, m, newHandle), OpRead)
		if err != nil {
			t.Fatal(err)
		}
		data, err := newLease.Read()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, []byte("rebuilt")) {
			t.Fatalf("round %d: reused object was modified: %q", i, data)
		}
		if err := m.DeleteObject(exportID, stable); err != nil {
			t.Fatal(err)
		}
	}
}
