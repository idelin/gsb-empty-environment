package quota

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	ta TenantID = "tenant-a"
	tb TenantID = "tenant-b"
)

func newTestManager(t *testing.T, aLimit, bLimit int64) *Manager {
	t.Helper()
	m := NewManager()
	if err := m.AddTenant(ta, aLimit); err != nil {
		t.Fatalf("AddTenant a: %v", err)
	}
	if err := m.AddTenant(tb, bLimit); err != nil {
		t.Fatalf("AddTenant b: %v", err)
	}
	return m
}

func mustUsage(t *testing.T, m *Manager, id TenantID) int64 {
	t.Helper()
	used, _, err := m.Usage(id)
	if err != nil {
		t.Fatalf("Usage(%s): %v", id, err)
	}
	return used
}

// assertInvariants checks the central billing invariants:
//   - every registered object has at least one live reference, and vice
//     versa (an unreferenced object must have been reaped);
//   - every directory entry points at a registered object;
//   - each tenant's used bytes equal the sum of its live objects' sizes.
func assertInvariants(t *testing.T, m *Manager) {
	t.Helper()

	objRefs := make(map[ObjectID]int)
	for id, obj := range m.objects {
		if !obj.alive() {
			t.Errorf("object %d registered with no references (links=%d handles=%d)",
				id, obj.links, obj.handles)
		}
		objRefs[id] = obj.links
	}

	linkCount := make(map[ObjectID]int)
	for id, tn := range m.tenants {
		var want int64
		seen := make(map[ObjectID]bool)
		for name, obj := range tn.names {
			if _, ok := m.objects[obj.id]; !ok {
				t.Errorf("tenant %s name %q points at reaped object %d", id, name, obj.id)
			}
			if obj.owner != id {
				t.Errorf("tenant %s names object %d owned by %s", id, obj.id, obj.owner)
			}
			linkCount[obj.id]++
			if !seen[obj.id] {
				seen[obj.id] = true
				want += obj.size
			}
		}
		for oid, got := range linkCount {
			obj := m.objects[oid]
			if obj == nil {
				t.Errorf("tenant %s references missing object %d", id, oid)
				continue
			}
			if got > obj.links {
				t.Errorf("object %d has %d directory entries but links=%d", oid, got, obj.links)
			}
		}
		for _, obj := range m.objects {
			if obj.owner == id && !seen[obj.id] {
				want += obj.size
			}
		}
		if tn.used != want {
			t.Errorf("tenant %s used=%d, want %d (sum of live object sizes)", id, tn.used, want)
		}
		if tn.used > tn.limit {
			t.Errorf("tenant %s used=%d exceeds limit=%d", id, tn.used, tn.limit)
		}
	}

	for id, obj := range m.objects {
		total := 0
		for _, tn := range m.tenants {
			for _, named := range tn.names {
				if named.id == id {
					total++
				}
			}
		}
		if total != obj.links {
			t.Errorf("object %d links=%d but namespace contains %d entries", id, obj.links, total)
		}
	}
}

// onceFault fires exactly once, when op/phase match the next expected point.
func onceFault(wantOp, wantPhase string) (Fault, *int) {
	fired := 0
	f := func(op, phase string, _ TenantID, _ string) error {
		if fired == 0 && op == wantOp && phase == wantPhase {
			fired++
			return errors.New("boom")
		}
		return nil
	}
	return f, &fired
}

func TestCreateAndQuotaLimit(t *testing.T) {
	m := newTestManager(t, 100, 100)

	if _, err := m.Create(ta, "f1", 60); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 60 {
		t.Fatalf("used=%d, want 60", got)
	}
	if _, err := m.Create(ta, "f2", 50); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-limit Create err=%v, want ErrQuotaExceeded", err)
	}
	if got := mustUsage(t, m, ta); got != 60 {
		t.Fatalf("failed Create changed usage to %d", got)
	}
	if _, err := m.Create(ta, "f1", 10); !errors.Is(err, ErrNameExists) {
		t.Fatalf("duplicate Create err=%v, want ErrNameExists", err)
	}
	assertInvariants(t, m)
}

func TestLinkRejectsCrossTenant(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	id, err := m.Create(ta, "f", 100)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A link into another tenant's namespace must be refused before any
	// state changes, and must leave the other tenant's quota untouched.
	if err := m.Link(id, tb, "alias"); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("cross-tenant Link err=%v, want ErrCrossTenant", err)
	}
	if got := mustUsage(t, m, tb); got != 0 {
		t.Fatalf("tenant-b used=%d after rejected link, want 0", got)
	}
	if _, ok := m.tenants[tb].names["alias"]; ok {
		t.Fatal("cross-tenant alias was created")
	}

	// Same-tenant links are allowed and do not double-bill the bytes.
	if err := m.Link(id, ta, "alias"); err != nil {
		t.Fatalf("same-tenant Link: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 100 {
		t.Fatalf("used after link=%d, want 100", got)
	}

	// Removing one of two links must not free the object.
	if err := m.Unlink(ta, "alias"); err != nil {
		t.Fatalf("Unlink alias: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 100 {
		t.Fatalf("used after removing one link=%d, want 100", got)
	}
	if _, ok := m.objects[id]; !ok {
		t.Fatal("object reaped while one link remains")
	}

	// The last link returns the bytes immediately when nothing is open.
	if err := m.Unlink(ta, "f"); err != nil {
		t.Fatalf("Unlink f: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used after last unlink=%d, want 0", got)
	}
	if _, ok := m.objects[id]; ok {
		t.Fatal("object not reaped after last link")
	}
	assertInvariants(t, m)
}

func TestRenameOverwriteTransaction(t *testing.T) {
	phases := []string{
		PhaseReserve,
		PhaseVictim,
		PhaseLinkadd,
		PhaseCommit,
	}
	for _, failPhase := range phases {
		t.Run(failPhase, func(t *testing.T) {
			m := newTestManager(t, 1000, 1000)
			srcID, err := m.Create(ta, "src", 100)
			if err != nil {
				t.Fatalf("Create src: %v", err)
			}
			victimID, err := m.Create(ta, "dst", 40)
			if err != nil {
				t.Fatalf("Create dst: %v", err)
			}

			fault, fired := onceFault(OpRename, failPhase)
			m.SetFault(fault)
			err = m.Rename(ta, "src", "dst")
			m.SetFault(nil)
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("Rename err=%v, want ErrInjected", err)
			}
			if *fired != 1 {
				t.Fatalf("fault fired %d times, want 1", *fired)
			}

			// Full rollback: both original entries, both objects and the
			// original quota must be present.
			if got := mustUsage(t, m, ta); got != 140 {
				t.Fatalf("used after failed rename=%d, want 140", got)
			}
			if len(m.objects) != 2 {
				t.Fatalf("object count=%d, want 2", len(m.objects))
			}
			if m.tenants[ta].names["src"] == nil ||
				m.tenants[ta].names["src"].id != srcID {
				t.Fatal("src entry not restored")
			}
			if m.tenants[ta].names["dst"] == nil ||
				m.tenants[ta].names["dst"].id != victimID {
				t.Fatal("dst victim entry not restored")
			}
			if m.objects[srcID].links != 1 || m.objects[victimID].links != 1 {
				t.Fatalf("link counts not restored: src=%d victim=%d",
					m.objects[srcID].links, m.objects[victimID].links)
			}
			assertInvariants(t, m)

			// A retry without the fault must succeed exactly once.
			if err := m.Rename(ta, "src", "dst"); err != nil {
				t.Fatalf("retry Rename: %v", err)
			}
			if got := mustUsage(t, m, ta); got != 100 {
				t.Fatalf("used after overwrite=%d, want 100 (victim freed)", got)
			}
			if _, ok := m.objects[victimID]; ok {
				t.Fatal("victim with no open handle was not reaped")
			}
			if m.tenants[ta].names["dst"].id != srcID {
				t.Fatal("dst does not name the source after rename")
			}
			if _, ok := m.tenants[ta].names["src"]; ok {
				t.Fatal("src entry still present after rename")
			}
			assertInvariants(t, m)
		})
	}
}

func TestCreateFaultInjection(t *testing.T) {
	for _, failPhase := range []string{PhaseReserve, PhaseCommit} {
		t.Run(failPhase, func(t *testing.T) {
			m := newTestManager(t, 1000, 1000)
			fault, fired := onceFault(OpCreate, failPhase)
			m.SetFault(fault)
			_, err := m.Create(ta, "f", 100)
			m.SetFault(nil)
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("Create err=%v, want ErrInjected", err)
			}
			if *fired != 1 {
				t.Fatalf("fault fired %d times, want 1", *fired)
			}
			if got := mustUsage(t, m, ta); got != 0 {
				t.Fatalf("used after failed create=%d, want 0", got)
			}
			if len(m.objects) != 0 {
				t.Fatalf("object table=%v, want empty", m.objects)
			}
			if _, ok := m.tenants[ta].names["f"]; ok {
				t.Fatal("namespace entry survived failed create")
			}
			assertInvariants(t, m)

			// The failed attempt must not have consumed an ID or quota.
			id, err := m.Create(ta, "f", 100)
			if err != nil {
				t.Fatalf("retry Create: %v", err)
			}
			if id != 1 {
				t.Fatalf("object id=%d, want 1 (failed attempt consumed an ID)", id)
			}
			if got := mustUsage(t, m, ta); got != 100 {
				t.Fatalf("used=%d, want 100", got)
			}
			assertInvariants(t, m)
		})
	}
}

func TestUnlinkFaultInjection(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	id, err := m.Create(ta, "f", 100)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, failPhase := range []string{PhaseDetach, PhaseCommit} {
		fault, fired := onceFault(OpUnlink, failPhase)
		m.SetFault(fault)
		err = m.Unlink(ta, "f")
		m.SetFault(nil)
		if !errors.Is(err, ErrInjected) {
			t.Fatalf("phase %s: Unlink err=%v, want ErrInjected", failPhase, err)
		}
		if *fired != 1 {
			t.Fatalf("phase %s: fault fired %d times", failPhase, *fired)
		}
		if got := mustUsage(t, m, ta); got != 100 {
			t.Fatalf("phase %s: used=%d, want 100", failPhase, got)
		}
		if m.tenants[ta].names["f"] == nil || m.tenants[ta].names["f"].id != id {
			t.Fatalf("phase %s: directory entry not restored", failPhase)
		}
		if m.objects[id].links != 1 {
			t.Fatalf("phase %s: links=%d, want 1", failPhase, m.objects[id].links)
		}
		assertInvariants(t, m)
	}

	if err := m.Unlink(ta, "f"); err != nil {
		t.Fatalf("final Unlink: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used=%d, want 0", got)
	}
	assertInvariants(t, m)
}

func TestSetSizeShrinkReturnsBytes(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	if _, err := m.Create(ta, "f", 100); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h, err := m.Open(ta, "f")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.SetSize(h, 40); err != nil {
		t.Fatalf("SetSize shrink: %v", err)
	}
	if got := h.Size(); got != 40 {
		t.Fatalf("size=%d, want 40", got)
	}
	if got := mustUsage(t, m, ta); got != 40 {
		t.Fatalf("used=%d, want 40", got)
	}
	if err := m.SetSize(h, 90); err != nil {
		t.Fatalf("SetSize grow: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 90 {
		t.Fatalf("used=%d, want 90", got)
	}
	assertInvariants(t, m)
}

func TestTenantIsolation(t *testing.T) {
	m := newTestManager(t, 100, 100)
	if _, err := m.Create(ta, "f", 60); err != nil {
		t.Fatalf("Create in a: %v", err)
	}
	if _, err := m.Create(tb, "f", 80); err != nil {
		t.Fatalf("Create in b: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 60 {
		t.Fatalf("tenant-a used=%d, want 60", got)
	}
	if got := mustUsage(t, m, tb); got != 80 {
		t.Fatalf("tenant-b used=%d, want 80", got)
	}
	if err := m.Unlink(tb, "f"); err != nil {
		t.Fatalf("Unlink in b: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 60 {
		t.Fatalf("tenant-a changed to %d after tenant-b unlink", got)
	}
	assertInvariants(t, m)
}

func ExampleManager() {
	m := NewManager()
	_ = m.AddTenant("acme", 1_000)
	id, _ := m.Create("acme", "report.bin", 250)
	h, _ := m.Open("acme", "report.bin")
	_ = m.Unlink("acme", "report.bin")
	used, _, _ := m.Usage("acme")
	fmt.Println("charged while open:", used)
	_ = m.Link(id, "acme", "report.bin") // same tenant: allowed
	_ = h.Close()
	used, _, _ = m.Usage("acme")
	fmt.Println("charged after close:", used)
	// Output:
	// charged while open: 250
	// charged after close: 250
}

func TestRenameOverwriteOpenVictim(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	if _, err := m.Create(ta, "src", 100); err != nil {
		t.Fatalf("Create src: %v", err)
	}
	victimID, err := m.Create(ta, "dst", 40)
	if err != nil {
		t.Fatalf("Create dst: %v", err)
	}
	vh, err := m.Open(ta, "dst")
	if err != nil {
		t.Fatalf("Open victim: %v", err)
	}

	if err := m.Rename(ta, "src", "dst"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// The overwritten victim lost its directory entry but stays open: its
	// physical bytes remain charged until the handle closes.
	if got := mustUsage(t, m, ta); got != 140 {
		t.Fatalf("used with open victim=%d, want 140", got)
	}
	if _, ok := m.objects[victimID]; !ok {
		t.Fatal("open victim was reaped during overwrite")
	}

	if err := vh.Close(); err != nil {
		t.Fatalf("Close victim: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 100 {
		t.Fatalf("used after victim close=%d, want 100", got)
	}
	if _, ok := m.objects[victimID]; ok {
		t.Fatal("victim not reaped after final close")
	}
	assertInvariants(t, m)
}

func TestGrowTransactionFaultInjection(t *testing.T) {
	for _, failPhase := range []string{PhaseReserve, PhaseCharge} {
		t.Run(failPhase, func(t *testing.T) {
			m := newTestManager(t, 1000, 1000)
			if _, err := m.Create(ta, "f", 100); err != nil {
				t.Fatalf("Create: %v", err)
			}
			h, err := m.Open(ta, "f")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			fault, fired := onceFault(OpGrow, failPhase)
			m.SetFault(fault)
			err = m.Grow(h, 50)
			m.SetFault(nil)
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("Grow err=%v, want ErrInjected", err)
			}
			if *fired != 1 {
				t.Fatalf("fault fired %d times, want 1", *fired)
			}
			if got := h.Size(); got != 100 {
				t.Fatalf("size after failed grow=%d, want 100", got)
			}
			if got := mustUsage(t, m, ta); got != 100 {
				t.Fatalf("used after failed grow=%d, want 100", got)
			}
			assertInvariants(t, m)

			// Retry succeeds and charges exactly the extension once.
			if err := m.Grow(h, 50); err != nil {
				t.Fatalf("retry Grow: %v", err)
			}
			if got := h.Size(); got != 150 {
				t.Fatalf("size=%d, want 150", got)
			}
			if got := mustUsage(t, m, ta); got != 150 {
				t.Fatalf("used=%d, want 150", got)
			}

			// A grow that would exceed the limit also leaves no residue.
			if err := m.Grow(h, 1_000_000); !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("over-limit Grow err=%v, want ErrQuotaExceeded", err)
			}
			if got := h.Size(); got != 150 {
				t.Fatalf("size after rejected grow=%d, want 150", got)
			}
			if got := mustUsage(t, m, ta); got != 150 {
				t.Fatalf("used after rejected grow=%d, want 150", got)
			}
			assertInvariants(t, m)
		})
	}
}

func TestUnlinkedOpenHandleKeepsBytesCharged(t *testing.T) {
	m := newTestManager(t, 1000, 1000)

	id, err := m.Create(ta, "secret", 100)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h, err := m.Open(ta, "secret")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Delete the only directory entry while the handle is open: the physical
	// bytes must remain charged against the tenant.
	if err := m.Unlink(ta, "secret"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 100 {
		t.Fatalf("used after unlink=%d, want 100", got)
	}
	if _, ok := m.objects[id]; !ok {
		t.Fatal("object reaped while a handle was open")
	}
	if _, err := m.Open(ta, "secret"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open after unlink err=%v, want ErrNotFound", err)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used after close=%d, want 0", got)
	}
	if _, ok := m.objects[id]; ok {
		t.Fatal("object not reaped after last reference closed")
	}
	if err := h.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("double Close err=%v, want ErrClosed", err)
	}
	assertInvariants(t, m)
}

func TestConcurrentCloseReclaimsOnce(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	if _, err := m.Create(ta, "f", 120); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 64
	handles := make([]*Handle, n)
	for i := range handles {
		h, err := m.Open(ta, "f")
		if err != nil {
			t.Fatalf("Open %d: %v", i, err)
		}
		handles[i] = h
	}
	if err := m.Unlink(ta, "f"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if got := mustUsage(t, m, ta); got != 120 {
		t.Fatalf("used with open handles=%d, want 120", got)
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for _, h := range handles {
		h := h
		go func() {
			defer wg.Done()
			errs <- h.Close()
		}()
	}
	wg.Wait()
	close(errs)

	okCloses := 0
	for err := range errs {
		if err == nil {
			okCloses++
		} else if !errors.Is(err, ErrClosed) {
			t.Fatalf("unexpected Close error: %v", err)
		}
	}
	if okCloses != n {
		t.Fatalf("successful closes=%d, want %d", okCloses, n)
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used after all closes=%d, want 0", got)
	}
	if len(m.objects) != 0 {
		t.Fatalf("object table=%v, want empty", m.objects)
	}

	// Every subsequent close must be a no-op.
	for _, h := range handles {
		if err := h.Close(); !errors.Is(err, ErrClosed) {
			t.Fatalf("repeated Close err=%v, want ErrClosed", err)
		}
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used grew after repeated closes: %d", got)
	}
	assertInvariants(t, m)
}

func TestConcurrentCloseOnSharedHandle(t *testing.T) {
	m := newTestManager(t, 1000, 1000)
	if _, err := m.Create(ta, "f", 70); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h, err := m.Open(ta, "f")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := m.Unlink(ta, "f"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}

	const n = 32
	var wg sync.WaitGroup
	results := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = h.Close()
		}(i)
	}
	wg.Wait()

	successes, duplicates := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrClosed):
			duplicates++
		default:
			t.Fatalf("unexpected Close error: %v", err)
		}
	}
	if successes != 1 || duplicates != n-1 {
		t.Fatalf("successes=%d duplicates=%d, want 1 and %d", successes, duplicates, n-1)
	}
	if got := mustUsage(t, m, ta); got != 0 {
		t.Fatalf("used=%d, want 0", got)
	}
	assertInvariants(t, m)
}
