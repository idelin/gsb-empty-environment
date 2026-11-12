package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustOpen(t *testing.T, iso *Isolation, tenant TenantID) *Session {
	t.Helper()
	session, err := iso.OpenSession(tenant)
	if err != nil {
		t.Fatalf("OpenSession(%q): %v", tenant, err)
	}
	return session
}

func mustLogin(t *testing.T, session *Session, pin string) {
	t.Helper()
	if err := session.Login(pin); err != nil {
		t.Fatalf("Login(%q): %v", session.Tenant(), err)
	}
}

func mustCreateToken(t *testing.T, session *Session, attrs Attributes) Handle {
	t.Helper()
	handle, err := session.CreateTokenObject(attrs)
	if err != nil {
		t.Fatalf("CreateTokenObject(%q): %v", session.Tenant(), err)
	}
	return handle
}

func mustCreateSession(t *testing.T, session *Session, attrs Attributes) Handle {
	t.Helper()
	handle, err := session.CreateSessionObject(attrs)
	if err != nil {
		t.Fatalf("CreateSessionObject(%q): %v", session.Tenant(), err)
	}
	return handle
}

func boolPtr(v bool) *bool { return &v }

func requireSameNotFound(t *testing.T, err error) {
	t.Helper()
	if err != ErrObjectNotFound {
		t.Fatalf("got error %v, want exact ErrObjectNotFound", err)
	}
}

// TestCrossTenantHandleGuessing covers the proxy's most important oracle:
// a handle belonging to tenant A must be unusable by tenant B, and B must get
// exactly the same answer it gets for a handle that never existed.
func TestCrossTenantHandleGuessing(t *testing.T) {
	iso := NewIsolation()
	a := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")
	mustLogin(t, a, "pin-a")
	mustLogin(t, b, "pin-b")

	aPrivate := mustCreateToken(t, a, Attributes{
		Class:   ClassPrivateKey,
		Label:   "a-private-key",
		ID:      []byte("key-a"),
		Value:   []byte("secret-a"),
		Private: true,
	})
	aSessionObject := mustCreateSession(t, a, Attributes{
		Class: ClassSecretKey,
		Label: "a-session-secret",
		Value: []byte("session-secret-a"),
	})

	const neverIssued Handle = 0x9e3779b97f4a7c15
	for _, handle := range []Handle{aPrivate, aSessionObject, neverIssued, 0} {
		if err := b.DestroyObject(handle); err != ErrObjectNotFound {
			t.Fatalf("tenant B DestroyObject(%#x): got %v, want ErrObjectNotFound", uint64(handle), err)
		}
	}

	for _, handle := range []Handle{aPrivate, aSessionObject, neverIssued, 0} {
		_, foreignErr := b.GetObject(handle)
		_, unknownErr := b.GetObject(neverIssued)
		requireSameNotFound(t, foreignErr)
		requireSameNotFound(t, unknownErr)
		if foreignErr.Error() != unknownErr.Error() {
			t.Fatalf("foreign and unknown handle errors differ: %q vs %q", foreignErr, unknownErr)
		}
	}

	objects, err := b.FindObjects(Filter{})
	if err != nil {
		t.Fatalf("tenant B FindObjects: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("tenant B can enumerate %d objects owned by tenant A", len(objects))
	}

	// The same tenant's other session may read the token object but must not
	// see the session object created by the first session.
	a2 := mustOpen(t, iso, "tenant-a")
	if _, err := a2.GetObject(aPrivate); err != nil {
		t.Fatalf("same-tenant session cannot read token object: %v", err)
	}
	if _, err := a2.GetObject(aSessionObject); err != ErrObjectNotFound {
		t.Fatalf("same-tenant peer session resolved a session object: %v", err)
	}

	// A's own session keeps legitimate access to both objects.
	if _, err := a.GetObject(aPrivate); err != nil {
		t.Fatalf("owner lost access to token object: %v", err)
	}
	if _, err := a.GetObject(aSessionObject); err != nil {
		t.Fatalf("owner lost access to session object: %v", err)
	}
}

// TestLoginStateIsTenantScoped verifies that another tenant cannot borrow a
// logged-in tenant's token visibility, even when it logs in itself.
func TestLoginStateIsTenantScoped(t *testing.T) {
	iso := NewIsolation()
	a := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")

	if _, err := a.CreateTokenObject(Attributes{Label: "before-login"}); err != ErrNotLoggedIn {
		t.Fatalf("token object before login: got %v, want ErrNotLoggedIn", err)
	}

	mustLogin(t, a, "pin-a")
	hA := mustCreateToken(t, a, Attributes{Label: "a-token", Value: []byte("a-value"), Private: true})

	if b.LoggedIn() {
		t.Fatal("tenant B inherited tenant A's login state")
	}
	if _, err := b.GetObject(hA); err != ErrObjectNotFound {
		t.Fatalf("unauthenticated tenant B read A's token object: %v", err)
	}
	if objects, err := b.FindObjects(Filter{}); err != nil || len(objects) != 0 {
		t.Fatalf("unauthenticated tenant B find: objects=%d err=%v", len(objects), err)
	}

	mustLogin(t, b, "pin-b")
	if !a.LoggedIn() || !b.LoggedIn() {
		t.Fatal("login should be independent per tenant")
	}
	if _, err := b.GetObject(hA); err != ErrObjectNotFound {
		t.Fatalf("logged-in tenant B read A's token object: %v", err)
	}

	hB := mustCreateToken(t, b, Attributes{Label: "b-token", Value: []byte("b-value"), Private: true})
	if _, err := a.GetObject(hB); err != ErrObjectNotFound {
		t.Fatalf("tenant A resolved tenant B's handle: %v", err)
	}

	if err := b.Logout(); err != nil {
		t.Fatalf("tenant B logout: %v", err)
	}
	if !a.LoggedIn() {
		t.Fatal("tenant B logout changed tenant A's login state")
	}
	if _, err := a.GetObject(hA); err != nil {
		t.Fatalf("tenant A lost access after tenant B logout: %v", err)
	}

	if err := a.Logout(); err != nil {
		t.Fatalf("tenant A logout: %v", err)
	}
	if a.LoggedIn() {
		t.Fatal("tenant A still logged in after logout")
	}
	if _, err := a.GetObject(hA); err != ErrObjectNotFound {
		t.Fatalf("logged-out tenant A still read token object: %v", err)
	}
}

// TestLogoutDestroysTenantSessionObjects verifies tenant-wide cleanup after a
// logout from any one of the tenant's sessions.
func TestLogoutDestroysTenantSessionObjects(t *testing.T) {
	iso := NewIsolation()
	a1 := mustOpen(t, iso, "tenant-a")
	a2 := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")
	mustLogin(t, a1, "pin-a")
	mustLogin(t, b, "pin-b")

	hA1 := mustCreateSession(t, a1, Attributes{Label: "a1-session"})
	hA2 := mustCreateSession(t, a2, Attributes{Label: "a2-session"})
	hToken := mustCreateToken(t, a1, Attributes{Label: "a-token", Value: []byte("persistent")})
	hB := mustCreateSession(t, b, Attributes{Label: "b-session"})

	if err := a2.Logout(); err != nil {
		t.Fatalf("logout through second session: %v", err)
	}

	for _, session := range []*Session{a1, a2} {
		if session.LoggedIn() {
			t.Fatal("tenant session remained logged in after tenant logout")
		}
		for _, handle := range []Handle{hA1, hA2, hToken} {
			if _, err := session.GetObject(handle); err != ErrObjectNotFound {
				t.Fatalf("session object/token handle %#x survived logout: %v", uint64(handle), err)
			}
		}
	}

	if _, err := b.GetObject(hB); err != nil {
		t.Fatalf("tenant B object was affected by tenant A logout: %v", err)
	}
	if !b.LoggedIn() {
		t.Fatal("tenant B was logged out by tenant A")
	}

	mustLogin(t, a1, "pin-a")
	if object, err := a1.GetObject(hToken); err != nil {
		t.Fatalf("token object did not survive re-login: %v", err)
	} else if !bytes.Equal(object.Value, []byte("persistent")) {
		t.Fatalf("token object value changed: %q", object.Value)
	}
	for _, handle := range []Handle{hA1, hA2} {
		if _, err := a1.GetObject(handle); err != ErrObjectNotFound {
			t.Fatalf("destroyed session object handle %#x became valid again: %v", uint64(handle), err)
		}
	}
}

// TestCloseSessionCleansUpOnlyItsObjects verifies legitimate close semantics
// and that token objects remain usable through other tenant sessions.
func TestCloseSessionCleansUpOnlyItsObjects(t *testing.T) {
	iso := NewIsolation()
	a1 := mustOpen(t, iso, "tenant-a")
	a2 := mustOpen(t, iso, "tenant-a")
	mustLogin(t, a1, "pin-a")

	hSession := mustCreateSession(t, a1, Attributes{Label: "session-only"})
	hToken := mustCreateToken(t, a1, Attributes{Label: "token", Value: []byte("token-value")})

	if err := a1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := a1.GetObject(hSession); err != ErrSessionClosed {
		t.Fatalf("closed session GetObject: got %v, want ErrSessionClosed", err)
	}
	if _, err := a2.GetObject(hSession); err != ErrObjectNotFound {
		t.Fatalf("peer session read closed session's object: %v", err)
	}
	if _, err := a2.GetObject(hToken); err != nil {
		t.Fatalf("peer session lost token object after close: %v", err)
	}
	if !a2.LoggedIn() {
		t.Fatal("closing one session logged out the tenant's other session")
	}

	if err := a2.Close(); err != nil {
		t.Fatalf("closing last session: %v", err)
	}
	// Closing the last session logs the tenant out but does not delete token
	// objects. They must be invisible until a new login.
	a3 := mustOpen(t, iso, "tenant-a")
	if a3.LoggedIn() {
		t.Fatal("tenant inherited a login after all sessions closed")
	}
	if _, err := a3.GetObject(hToken); err != ErrObjectNotFound {
		t.Fatalf("token object visible without login: %v", err)
	}
	mustLogin(t, a3, "pin-a")
	if _, err := a3.GetObject(hToken); err != nil {
		t.Fatalf("token object unavailable after new login: %v", err)
	}
}

// TestLegitimateUseAndSearch exercises the non-attack path and ensures handles
// and returned object snapshots cannot be used to mutate shared state.
func TestLegitimateUseAndSearch(t *testing.T) {
	iso := NewIsolation()
	a := mustOpen(t, iso, "tenant-a")
	mustLogin(t, a, "pin-a")

	attrs := Attributes{
		Class:   ClassPrivateKey,
		Label:   "signing-key",
		ID:      []byte("id-1"),
		Value:   []byte("original"),
		Private: true,
	}
	h := mustCreateToken(t, a, attrs)
	h2 := mustCreateSession(t, a, Attributes{Class: ClassData, Label: "notes", Value: []byte("note")})
	if h == 0 || h2 == 0 || h == h2 {
		t.Fatalf("invalid or duplicate handles: %#x, %#x", uint64(h), uint64(h2))
	}

	object, err := a.GetObject(h)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if object.Handle != h || object.Label != "signing-key" || !object.Token || !object.Private {
		t.Fatalf("unexpected object snapshot: %+v", object)
	}
	object.Value[0] = 'X'
	object.ID[0] = 'Y'
	fresh, err := a.GetObject(h)
	if err != nil {
		t.Fatalf("GetObject after mutating snapshot: %v", err)
	}
	if !bytes.Equal(fresh.Value, []byte("original")) || !bytes.Equal(fresh.ID, []byte("id-1")) {
		t.Fatalf("returned snapshot mutated stored object: %+v", fresh)
	}

	tests := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"all", Filter{}, 2},
		{"class", Filter{Class: ClassPrivateKey}, 1},
		{"label", Filter{Label: "notes"}, 1},
		{"id", Filter{ID: []byte("id-1")}, 1},
		{"token", Filter{Token: boolPtr(true)}, 1},
		{"session", Filter{Token: boolPtr(false)}, 1},
		{"private", Filter{Private: boolPtr(true)}, 1},
		{"no-match", Filter{Label: "missing"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects, err := a.FindObjects(tt.filter)
			if err != nil {
				t.Fatalf("FindObjects: %v", err)
			}
			if len(objects) != tt.want {
				t.Fatalf("FindObjects returned %d objects, want %d: %+v", len(objects), tt.want, objects)
			}
		})
	}

	if err := a.DestroyObject(h); err != nil {
		t.Fatalf("DestroyObject token: %v", err)
	}
	if _, err := a.GetObject(h); err != ErrObjectNotFound {
		t.Fatalf("destroyed token handle still resolves: %v", err)
	}
	if err := a.DestroyObject(h); err != ErrObjectNotFound {
		t.Fatalf("double destroy: got %v, want ErrObjectNotFound", err)
	}
	if err := a.DestroyObject(h2); err != nil {
		t.Fatalf("DestroyObject session: %v", err)
	}
	if objects, err := a.FindObjects(Filter{}); err != nil || len(objects) != 0 {
		t.Fatalf("objects remain after destroy: objects=%d err=%v", len(objects), err)
	}
}

func TestPINValidationAndRegistration(t *testing.T) {
	iso := NewIsolation()
	if err := iso.RegisterTenant("tenant-a", "1111"); err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := iso.RegisterTenant("tenant-b", "2222"); err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := iso.RegisterTenant("tenant-a", "other"); err != ErrTenantExists {
		t.Fatalf("duplicate RegisterTenant: got %v, want ErrTenantExists", err)
	}
	if _, err := iso.OpenSession(""); err != ErrInvalidTenant {
		t.Fatalf("empty tenant: got %v, want ErrInvalidTenant", err)
	}

	a := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")
	if err := a.Login("wrong"); err != ErrInvalidPIN {
		t.Fatalf("tenant A wrong PIN: got %v, want ErrInvalidPIN", err)
	}
	if a.LoggedIn() {
		t.Fatal("failed login changed tenant A state")
	}
	mustLogin(t, b, "2222")
	mustLogin(t, a, "1111")
	if !a.LoggedIn() || !b.LoggedIn() {
		t.Fatal("independent valid logins failed")
	}
	if err := b.Logout(); err != nil {
		t.Fatalf("tenant B logout: %v", err)
	}
	if !a.LoggedIn() {
		t.Fatal("tenant B logout affected tenant A")
	}
}

func TestFindExcludesForeignAndUnauthenticatedTokenObjects(t *testing.T) {
	iso := NewIsolation()
	a := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")
	mustLogin(t, a, "pin-a")
	mustLogin(t, b, "pin-b")

	hA := mustCreateToken(t, a, Attributes{Label: "shared-label", Value: []byte("a")})
	hB := mustCreateToken(t, b, Attributes{Label: "shared-label", Value: []byte("b")})
	if hA == hB {
		t.Skip("random handle collision; rerun")
	}

	objects, err := b.FindObjects(Filter{Label: "shared-label"})
	if err != nil {
		t.Fatalf("FindObjects: %v", err)
	}
	if len(objects) != 1 || objects[0].Handle != hB || !bytes.Equal(objects[0].Value, []byte("b")) {
		t.Fatalf("tenant B search leaked or lost objects: %+v", objects)
	}
}

func TestRemoveTenantDestroysAllTenantState(t *testing.T) {
	iso := NewIsolation()
	a := mustOpen(t, iso, "tenant-a")
	b := mustOpen(t, iso, "tenant-b")
	mustLogin(t, a, "pin-a")
	mustLogin(t, b, "pin-b")

	hSession := mustCreateSession(t, a, Attributes{Label: "a-session"})
	hToken := mustCreateToken(t, a, Attributes{Label: "a-token"})
	hB := mustCreateToken(t, b, Attributes{Label: "b-token"})

	if err := iso.RemoveTenant("tenant-a"); err != nil {
		t.Fatalf("RemoveTenant: %v", err)
	}
	if _, err := a.GetObject(hSession); err != ErrSessionClosed {
		t.Fatalf("removed tenant session still usable: %v", err)
	}
	if _, err := b.GetObject(hB); err != nil {
		t.Fatalf("tenant B state was removed with tenant A: %v", err)
	}

	// A fresh tenant with the old name must not inherit token objects or
	// handles from the removed tenant.
	a2 := mustOpen(t, iso, "tenant-a")
	mustLogin(t, a2, "pin-a")
	if _, err := a2.GetObject(hToken); err != ErrObjectNotFound {
		t.Fatalf("recreated tenant inherited old token handle: %v", err)
	}
	if objects, err := a2.FindObjects(Filter{}); err != nil || len(objects) != 0 {
		t.Fatalf("recreated tenant inherited old objects: objects=%d err=%v", len(objects), err)
	}
}

func TestAliasesRemainOpaque(t *testing.T) {
	if !errors.Is(ErrUnknownHandle, ErrObjectNotFound) ||
		!errors.Is(ErrHandleNotFound, ErrObjectNotFound) {
		t.Fatal("handle error aliases are not identical to ErrObjectNotFound")
	}
	if ErrUnknownHandle.Error() != "netsec: object not found" {
		t.Fatalf("unexpected unknown handle text: %q", ErrUnknownHandle)
	}
}

// TestConcurrentTenantIsolation is intended to be run under -race. It also
// checks that heavy activity on one tenant does not grant handles to another.
func TestConcurrentTenantIsolation(t *testing.T) {
	iso := NewIsolation()
	mustLogin(t, mustOpen(t, iso, "tenant-a"), "pin-a")
	mustLogin(t, mustOpen(t, iso, "tenant-b"), "pin-b")

	const workers = 8
	const iterations = 100
	handles := make(chan Handle, workers*iterations)
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			session, err := iso.OpenSession("tenant-a")
			if err != nil {
				t.Error(err)
				return
			}
			for n := 0; n < iterations; n++ {
				h, err := session.CreateSessionObject(Attributes{
					Label: fmt.Sprintf("obj-%d-%d", worker, n),
					Value: []byte("v"),
				})
				if err != nil {
					t.Error(err)
					return
				}
				handles <- h
				if _, err := session.GetObject(h); err != nil {
					t.Errorf("owner cannot read handle: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(handles)

	b := mustOpen(t, iso, "tenant-b")
	seen := make(map[Handle]struct{}, workers*iterations)
	for h := range handles {
		if h == 0 {
			t.Fatal("handle generator returned zero")
		}
		if _, duplicate := seen[h]; duplicate {
			t.Fatalf("duplicate handle %#x issued inside one tenant", uint64(h))
		}
		seen[h] = struct{}{}
		if _, err := b.GetObject(h); err != ErrObjectNotFound {
			t.Fatalf("tenant B resolved tenant A handle %#x: %v", uint64(h), err)
		}
	}
}
