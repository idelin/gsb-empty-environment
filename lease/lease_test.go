package lease

import (
	"errors"
	"testing"
	"time"
)

func testManager(overlap time.Duration) (*Manager, *FakeClock) {
	clock := NewFakeClock(time.Unix(1_700_000_000, 0))
	return NewManager(Options{Now: clock.Now, Overlap: overlap}), clock
}

func TestIssueAndRoute(t *testing.T) {
	m, _ := testManager(time.Minute)
	l, err := m.Issue(1, 100, 4, 9, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if l.Generation != 1 || !l.Current() {
		t.Fatalf("want current generation 1, got %+v", l)
	}
	got, err := m.Route(100, 4)
	if err != nil || got != l {
		t.Fatalf("route: got %+v, err %v", got, err)
	}
	if _, err := m.Route(100, 5); !errors.Is(err, ErrNoLease) {
		t.Fatalf("unknown route: want ErrNoLease, got %v", err)
	}
	if _, err := m.Issue(1, 100, 4, 7, 0, 10); !errors.Is(err, ErrRouteInUse) {
		t.Fatalf("re-issue on busy route: want ErrRouteInUse, got %v", err)
	}
	if _, err := m.Issue(1, 200, 4, 9, 0, 10); !errors.Is(err, ErrRequestLeased) {
		t.Fatalf("re-issue of leased request: want ErrRequestLeased, got %v", err)
	}
	if _, err := m.Issue(1, 200, 4, 9, 0, uint32(SeqHalf)); !errors.Is(err, ErrInvalidSpan) {
		t.Fatalf("invalid span: want ErrInvalidSpan, got %v", err)
	}
}

func TestStreamNumberReuseAcrossConnections(t *testing.T) {
	m, _ := testManager(time.Minute)
	// Same stream number 4 on two different connections: distinct
	// namespaces, so both leases must coexist.
	a, err := m.Issue(1, 100, 4, 9, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Issue(1, 200, 4, 10, 5000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("leases must be distinct objects")
	}
	gotA, err := m.Route(100, 4)
	if err != nil || gotA != a {
		t.Fatalf("route conn 100: got %+v, err %v", gotA, err)
	}
	gotB, err := m.Route(200, 4)
	if err != nil || gotB != b {
		t.Fatalf("route conn 200: got %+v, err %v", gotB, err)
	}
}

func TestSameAuthNumberAcrossTenants(t *testing.T) {
	m, _ := testManager(time.Minute)
	// Same business authorization number 9 for two tenants: distinct
	// requests, both leasable.
	if _, err := m.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Issue(2, 200, 4, 9, 1000, 100); err != nil {
		t.Fatalf("second tenant with same auth id: %v", err)
	}
}

func TestMigrateOverlapAndExpiry(t *testing.T) {
	m, clock := testManager(30 * time.Second)
	old, err := m.Issue(1, 100, 4, 9, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	newL, err := m.Migrate(1, 9, 200, 4, 2000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if newL.Generation != 2 || !newL.Current() {
		t.Fatalf("want current generation 2, got %+v", newL)
	}
	if old.Current() {
		t.Fatal("old lease must be superseded")
	}
	// During the overlap window both paths route.
	if got, err := m.Route(100, 4); err != nil || got != old {
		t.Fatalf("old path in overlap: got %+v, err %v", got, err)
	}
	if got, err := m.Route(200, 4); err != nil || got != newL {
		t.Fatalf("new path: got %+v, err %v", got, err)
	}
	// After the window closes the old path is expired.
	clock.Advance(31 * time.Second)
	if _, err := m.Route(100, 4); !errors.Is(err, ErrOverlapExpired) {
		t.Fatalf("expired old path: want ErrOverlapExpired, got %v", err)
	}
	if got, err := m.Route(200, 4); err != nil || got != newL {
		t.Fatalf("new path after expiry: got %+v, err %v", got, err)
	}
}

func TestCloseEndsOldPathImmediately(t *testing.T) {
	m, _ := testManager(time.Hour)
	old, err := m.Issue(1, 100, 4, 9, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Migrate(1, 9, 200, 4, 2000, 100); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Route(100, 4); !errors.Is(err, ErrNoLease) {
		t.Fatalf("closed old path: want ErrNoLease, got %v", err)
	}
}
