package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	tenantA TenantID = "tenant-a"
	tenantB TenantID = "tenant-b"
)

// testRing builds an Authorizer with two tenants sharing one ring.
//
// Fixed file tables deliberately overlap on host fd 11 but map different
// slots to it, so FD_FIXED resolution is tenant-specific:
//
//	tenant A: slot 0 -> 11, slot 1 -> 12
//	tenant B: slot 0 -> 13, slot 1 -> 11
func testRing(t *testing.T) (*Authorizer, map[string]uint64) {
	t.Helper()
	auth := NewAuthorizer()
	auth.RegisterFixedFiles(tenantA, map[int32]int32{0: 11, 1: 12})
	auth.RegisterFixedFiles(tenantB, map[int32]int32{0: 13, 1: 11})

	ids := make(map[string]uint64)
	add := func(name string, req Request) {
		ids[name] = auth.AddRequest(req)
	}
	add("a1", Request{Tenant: tenantA, UserData: 1001, FD: 11, Opcode: 22})
	add("a2", Request{Tenant: tenantA, UserData: 1002, FD: 12, Opcode: 23})
	add("a3", Request{Tenant: tenantA, UserData: 1003, FD: 12, Opcode: 22})
	add("a4", Request{Tenant: tenantA, UserData: 1004, FD: 12, Opcode: 23})
	add("b1", Request{Tenant: tenantB, UserData: 2001, FD: 11, Opcode: 22})
	add("b2", Request{Tenant: tenantB, UserData: 2002, FD: 13, Opcode: 23})
	add("b3", Request{Tenant: tenantB, UserData: 2003, FD: 11, Opcode: 24})

	if auth.PendingCount(tenantA) != 4 || auth.PendingCount(tenantB) != 3 {
		t.Fatalf("unexpected initial counts: A=%d B=%d",
			auth.PendingCount(tenantA), auth.PendingCount(tenantB))
	}
	return auth, ids
}

func pendingIDs(auth *Authorizer, tenant TenantID) map[uint64]bool {
	out := make(map[uint64]bool)
	for _, req := range auth.PendingRequests(tenant) {
		out[req.ID] = true
	}
	return out
}

// ANY must only wild-card the caller's own requests and leave every other
// tenant's request intact.
func TestSyncCancel_ANY_WildcardScopedToCaller(t *testing.T) {
	auth, ids := testRing(t)

	n, err := auth.SyncCancel(CancelReq{
		Caller:  tenantA,
		Flags:   CancelAny,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ANY cancel: unexpected error %v", err)
	}
	if n != 4 {
		t.Fatalf("ANY cancel: got %d cancelled, want 4", n)
	}
	if auth.PendingCount(tenantA) != 0 {
		t.Fatalf("tenant A should have no pending requests, got %d",
			auth.PendingCount(tenantA))
	}
	if auth.PendingCount(tenantB) != 3 {
		t.Fatalf("tenant B requests must be untouched, got %d pending",
			auth.PendingCount(tenantB))
	}

	// A second ANY for A is a plain no-match, indistinguishable from any other
	// empty tenant.
	if _, err := auth.SyncCancel(CancelReq{Caller: tenantA, Flags: CancelAny}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("second ANY: got %v, want ErrNoMatch", err)
	}

	// ANY cannot be combined with ALL or concrete selectors.
	n, err = auth.SyncCancel(CancelReq{Caller: tenantB, Flags: CancelAny | CancelAll})
	if !errors.Is(err, ErrInvalidFlags) {
		t.Fatalf("ANY|ALL: got (%d, %v), want ErrInvalidFlags", n, err)
	}
	if auth.PendingCount(tenantB) != 3 {
		t.Fatalf("rejected ANY|ALL must not remove requests, B=%d",
			auth.PendingCount(tenantB))
	}

	// B's own wildcard reaches every B request but none of A's (already empty).
	n, err = auth.SyncCancel(CancelReq{Caller: tenantB, Flags: CancelAny})
	if err != nil || n != 3 {
		t.Fatalf("ANY for B: got (%d, %v), want (3, nil)", n, err)
	}
	if ids["b1"] == 0 {
		t.Fatal("expected recorded request id for b1")
	}
}

// A user_data value that belongs to another tenant must not match, and the
// returned error must be the same no-match the caller gets for a value nobody
// owns.
func TestSyncCancel_OtherTenantUserDataNotReachable(t *testing.T) {
	auth, ids := testRing(t)

	n, err := auth.SyncCancel(CancelReq{
		Caller:   tenantA,
		Flags:    CancelUserData,
		UserData: 2001, // belongs to tenant B's b1
	})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("cross-tenant user_data: got (%d, %v), want ErrNoMatch", n, err)
	}
	if n != 0 {
		t.Fatalf("cross-tenant user_data cancelled %d requests", n)
	}
	if !pendingIDs(auth, tenantB)[ids["b1"]] {
		t.Fatal("tenant B request b1 must still be in flight")
	}

	// Reference error for a user_data nobody owns: must be identical, so the
	// caller cannot probe whether another tenant owns a value.
	_, ghostErr := auth.SyncCancel(CancelReq{
		Caller:   tenantA,
		Flags:    CancelUserData,
		UserData: 999999,
	})
	if !errors.Is(ghostErr, ErrNoMatch) || ghostErr.Error() != err.Error() {
		t.Fatalf("leak: cross-tenant error %q differs from ghost error %q",
			err, ghostErr)
	}

	// The same value used by its real owner cancels exactly one request.
	n, err = auth.SyncCancel(CancelReq{
		Caller:   tenantB,
		Flags:    CancelUserData,
		UserData: 2001,
	})
	if err != nil || n != 1 {
		t.Fatalf("owner user_data cancel: got (%d, %v), want (1, nil)", n, err)
	}
	if auth.PendingCount(tenantB) != 2 {
		t.Fatalf("tenant B should have 2 pending, got %d", auth.PendingCount(tenantB))
	}
}

// FD_FIXED slots resolve through the caller's own fixed file table. The same
// slot number means different host fds for different tenants, and a shared
// host fd never exposes another tenant's request.
func TestSyncCancel_FDFixedResolvedAgainstCallerTable(t *testing.T) {
	t.Run("misaligned slot hits caller fd only", func(t *testing.T) {
		auth, ids := testRing(t)

		// Tenant A slot 1 -> host fd 12 (requests a2/a3/a4). It must not
		// resolve to fd 11 merely because tenant B's slot 1 points there.
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelFD | CancelFDFixed | CancelAll,
			FD:     1,
		})
		if err != nil || n != 3 {
			t.Fatalf("A fixed slot 1 ALL: got (%d, %v), want (3, nil)", n, err)
		}
		aPending := pendingIDs(auth, tenantA)
		if aPending[ids["a2"]] || aPending[ids["a3"]] || aPending[ids["a4"]] {
			t.Fatal("tenant A fd-12 requests should be cancelled")
		}
		if !aPending[ids["a1"]] {
			t.Fatal("tenant A request a1 on fd 11 must remain")
		}
		if auth.PendingCount(tenantB) != 3 {
			t.Fatalf("tenant B must be untouched, pending=%d", auth.PendingCount(tenantB))
		}
	})

	t.Run("shared host fd stays tenant scoped", func(t *testing.T) {
		auth, _ := testRing(t)

		// Both tenants can reach host fd 11: A via slot 0, B via slot 1.
		// Cancelling via A's slot 0 must never touch B's fd-11 requests.
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelFD | CancelFDFixed | CancelAll,
			FD:     0,
		})
		if err != nil || n != 1 {
			t.Fatalf("A fixed slot 0: got (%d, %v), want (1, nil)", n, err)
		}
		bFD11 := 0
		for _, req := range auth.PendingRequests(tenantB) {
			if req.FD == 11 {
				bFD11++
			}
		}
		if bFD11 != 2 {
			t.Fatalf("tenant B fd-11 requests must remain, found %d", bFD11)
		}
	})

	t.Run("unregistered slot rejected without probing others", func(t *testing.T) {
		auth, _ := testRing(t)
		_, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelFD | CancelFDFixed,
			FD:     7, // not in A's table
		})
		if !errors.Is(err, ErrInvalidFixedSlot) {
			t.Fatalf("unregistered slot: got %v, want ErrInvalidFixedSlot", err)
		}
		if auth.PendingCount(tenantA) != 4 || auth.PendingCount(tenantB) != 3 {
			t.Fatal("failed fixed-slot cancel must not remove any request")
		}
	})

	t.Run("plain FD does not use fixed table", func(t *testing.T) {
		auth, ids := testRing(t)
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantB,
			Flags:  CancelFD,
			FD:     13, // b2's open fd; also B slot 0's backing fd
		})
		if err != nil || n != 1 {
			t.Fatalf("plain FD cancel: got (%d, %v), want (1, nil)", n, err)
		}
		if pendingIDs(auth, tenantB)[ids["b2"]] {
			t.Fatal("b2 should be cancelled")
		}
	})
}

func TestSyncCancel_TimeoutEnforced(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		want    error
	}{
		{"negative", -time.Nanosecond, ErrInvalidTimeout},
		{"just over limit", MaxCancelTimeout + time.Nanosecond, ErrTimeoutTooLarge},
		{"far over limit", time.Minute, ErrTimeoutTooLarge},
		{"boundary accepted", MaxCancelTimeout, nil},
		{"zero accepted", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, _ := testRing(t)
			n, err := auth.SyncCancel(CancelReq{
				Caller:  tenantA,
				Flags:   CancelAny,
				Timeout: tc.timeout,
			})
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got (%d, %v), want error %v", n, err, tc.want)
				}
				if n != 0 || auth.PendingCount(tenantA) != 4 {
					t.Fatal("rejected timeout must not cancel anything")
				}
				return
			}
			if err != nil || n != 4 {
				t.Fatalf("accepted timeout: got (%d, %v), want (4, nil)", n, err)
			}
		})
	}
}

// A tenant cancelling its own requests gets accurate counts and can combine
// selectors; without ALL only the first match is cancelled.
func TestSyncCancel_CallersOwnRequests(t *testing.T) {
	t.Run("opcode cancels first match only by default", func(t *testing.T) {
		auth, ids := testRing(t)
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelOp,
			Opcode: 23, // a2 and a4
		})
		if err != nil || n != 1 {
			t.Fatalf("op cancel without ALL: got (%d, %v), want (1, nil)", n, err)
		}
		remaining := pendingIDs(auth, tenantA)
		if remaining[ids["a2"]] == remaining[ids["a4"]] {
			t.Fatal("exactly one of a2/a4 should remain after first-match cancel")
		}
	})

	t.Run("all opcode matches removed", func(t *testing.T) {
		auth, _ := testRing(t)
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelOp | CancelAll,
			Opcode: 23,
		})
		if err != nil || n != 2 {
			t.Fatalf("op ALL: got (%d, %v), want (2, nil)", n, err)
		}
		for _, req := range auth.PendingRequests(tenantA) {
			if req.Opcode == 23 {
				t.Fatalf("opcode 23 request %d still pending", req.ID)
			}
		}
		if auth.PendingCount(tenantB) != 3 {
			t.Fatal("tenant B must be untouched")
		}
	})

	t.Run("combined fd and opcode", func(t *testing.T) {
		auth, ids := testRing(t)
		n, err := auth.SyncCancel(CancelReq{
			Caller: tenantA,
			Flags:  CancelFD | CancelOp,
			FD:     12,
			Opcode: 23, // a2 and a4; a3 is fd 12 but opcode 22
		})
		if err != nil || n != 1 {
			t.Fatalf("fd+op: got (%d, %v), want (1, nil)", n, err)
		}
		remaining := pendingIDs(auth, tenantA)
		if !remaining[ids["a3"]] {
			t.Fatal("a3 has a different opcode and must remain")
		}
		if remaining[ids["a2"]] && remaining[ids["a4"]] {
			t.Fatal("one of a2/a4 should be cancelled")
		}
		if auth.PendingCount(tenantB) != 3 {
			t.Fatal("tenant B must be untouched")
		}
	})

	t.Run("remove request on normal completion", func(t *testing.T) {
		auth, ids := testRing(t)
		if !auth.RemoveRequest(tenantA, ids["a1"]) {
			t.Fatal("RemoveRequest should report the request as present")
		}
		if auth.RemoveRequest(tenantA, ids["a1"]) {
			t.Fatal("second RemoveRequest should report absence")
		}
		if auth.RemoveRequest(tenantB, ids["a1"]) {
			t.Fatal("tenant B must not remove tenant A's request")
		}
		if auth.PendingCount(tenantA) != 3 {
			t.Fatalf("tenant A should have 3 pending, got %d", auth.PendingCount(tenantA))
		}
	})
}

func TestSyncCancel_RejectsInvalidFlagCombinations(t *testing.T) {
	cases := []struct {
		name  string
		flags CancelFlag
	}{
		{"no selectors", 0},
		{"unknown flag bit", CancelFlag(1 << 20)},
		{"any with userdata", CancelAny | CancelUserData},
		{"any with fd", CancelAny | CancelFD},
		{"any with op", CancelAny | CancelOp},
		{"fd fixed without fd", CancelFDFixed},
		{"fd fixed all without fd", CancelFDFixed | CancelAll},
		{"all alone has no concrete selector", CancelAll},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, _ := testRing(t)
			n, err := auth.SyncCancel(CancelReq{
				Caller: tenantA,
				Flags:  tc.flags,
				FD:     0,
			})
			if !errors.Is(err, ErrInvalidFlags) {
				t.Fatalf("got (%d, %v), want ErrInvalidFlags", n, err)
			}
			if auth.PendingCount(tenantA) != 4 || auth.PendingCount(tenantB) != 3 {
				t.Fatal("rejected request must not modify the registry")
			}
		})
	}
}

// A tenant with no state at all gets the same no-match as an existing tenant
// whose selectors simply do not match, so tenant existence is not disclosed.
func TestSyncCancel_UnknownTenantRevealsNothing(t *testing.T) {
	auth, _ := testRing(t)
	ghost := TenantID("tenant-ghost")

	n, err := auth.SyncCancel(CancelReq{
		Caller:   ghost,
		Flags:    CancelUserData,
		UserData: 2001, // tenant B really owns this
	})
	if !errors.Is(err, ErrNoMatch) || n != 0 {
		t.Fatalf("unknown tenant probe: got (%d, %v), want (0, ErrNoMatch)", n, err)
	}
	n, err = auth.SyncCancel(CancelReq{Caller: ghost, Flags: CancelAny})
	if !errors.Is(err, ErrNoMatch) || n != 0 {
		t.Fatalf("unknown tenant ANY: got (%d, %v), want (0, ErrNoMatch)", n, err)
	}
	if auth.PendingCount(tenantA) != 4 || auth.PendingCount(tenantB) != 3 {
		t.Fatal("unknown tenant probing must not touch real tenants")
	}

	if _, err := auth.SyncCancel(CancelReq{Flags: CancelAny}); !errors.Is(err, ErrInvalidFlags) {
		t.Fatalf("empty caller: got %v, want ErrInvalidFlags", err)
	}
}

func TestSyncCancel_Concurrent(t *testing.T) {
	auth := NewAuthorizer()
	const goroutines = 16
	const perGo = 50
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		tenant := TenantID("t")
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGo; i++ {
				auth.AddRequest(Request{Tenant: tenant, UserData: uint64(i), FD: 7, Opcode: 1})
			}
		}()
	}

	// A second tenant continuously attempts to cancel the shared tenant's
	// requests; none of its attempts may ever succeed.
	attacker := TenantID("attacker")
	auth.AddRequest(Request{Tenant: attacker, UserData: 1, FD: 7})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGo; i++ {
				n, err := auth.SyncCancel(CancelReq{Caller: attacker, Flags: CancelAny})
				if err == nil && n != 1 {
					t.Errorf("attacker ANY cancelled %d requests, want only its own 1", n)
					return
				}
			}
		}()
	}

	wg.Wait()
	if got := auth.PendingCount("t"); got != goroutines*perGo {
		t.Fatalf("shared tenant pending=%d, want %d", got, goroutines*perGo)
	}
}
