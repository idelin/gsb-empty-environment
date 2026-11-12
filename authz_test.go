package netsec

import (
	"errors"
	"sync"
	"testing"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"

	historyA1 = "/dav/tenants/a/history"
	historyA2 = "/dav/tenants/a/history-alt"
	historyB1 = "/dav/tenants/b/history"

	resourceA1 = "/dav/tenants/a/files/report"
	resourceA2 = "/dav/tenants/a/files/notes"

	versionA11 = "/dav/tenants/a/history/versions/1"
	versionA12 = "/dav/tenants/a/history/versions/2"
	versionA21 = "/dav/tenants/a/history-alt/versions/1"
	versionB11 = "/dav/tenants/b/history/versions/1"

	workingA1 = "/dav/tenants/a/working/report"
)

var (
	alice   = Principal{Tenant: tenantA, Name: "alice"}
	bob     = Principal{Tenant: tenantA, Name: "bob"}
	mallory = Principal{Tenant: tenantB, Name: "mallory"}
)

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()

	a := NewAuthorizer()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture setup failed: %v", err)
		}
	}

	must(a.AddHistory(History{Href: historyA1, Tenant: tenantA}))
	must(a.AddHistory(History{Href: historyA2, Tenant: tenantA}))
	must(a.AddHistory(History{Href: historyB1, Tenant: tenantB}))

	must(a.AddControlledResource(resourceA1, historyA1))
	must(a.AddControlledResource(resourceA2, historyA2))

	must(a.AddVersion(versionA11, historyA1))
	must(a.AddVersion(versionA12, historyA1))
	must(a.AddVersion(versionA21, historyA2))
	must(a.AddVersion(versionB11, historyB1))

	must(a.AddWorkingResource(workingA1, historyA1))
	return a
}

func requireError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func TestAuthorizeUpdateCrossTenantVersionIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)

	// Even an explicitly granted read must not cross the tenant boundary.
	if err := a.GrantVersionRead(alice, versionB11); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeUpdate(alice, resourceA1, versionB11)
	requireError(t, err, ErrNotFound)

	// The response must be indistinguishable from a version that does not
	// exist at all.
	missing := a.AuthorizeUpdate(alice, resourceA1, "/dav/tenants/a/history/versions/999")
	requireError(t, missing, ErrNotFound)
	if err.Error() != missing.Error() {
		t.Fatalf("cross-tenant error %q leaks information versus missing error %q", err, missing)
	}
}

func TestAuthorizeUpdateOtherHistoryInSameTenantIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantVersionRead(alice, versionA21); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeUpdate(alice, resourceA1, versionA21)
	requireError(t, err, ErrNotFound)
}

func TestAuthorizeUpdateWithoutVersionReadIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)

	err := a.AuthorizeUpdate(alice, resourceA1, versionA12)
	requireError(t, err, ErrNotFound)
}

func TestAuthorizeUpdateLegalVersion(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantVersionRead(alice, versionA12); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	if err := a.AuthorizeUpdate(alice, resourceA1, versionA12); err != nil {
		t.Fatalf("legal UPDATE denied: %v", err)
	}
}

func TestAuthorizeUpdateUnknownResourceIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantVersionRead(alice, versionA11); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeUpdate(alice, "/dav/tenants/a/files/missing", versionA11)
	requireError(t, err, ErrNotFound)
}

func TestLabelAddConflictDoesNotStealExistingLabel(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	}); err != nil {
		t.Fatalf("initial label add failed: %v", err)
	}

	err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA12,
	})
	requireError(t, err, ErrLabelExists)

	got, ok := a.LabelVersion(alice, historyA1, "release")
	if !ok || got != versionA11 {
		t.Fatalf("label was changed after failed add: got %q, exists=%v", got, ok)
	}
}

func TestLabelSetMovesExistingLabel(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	}); err != nil {
		t.Fatalf("initial label add failed: %v", err)
	}
	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelSet, Name: "release", VersionHref: versionA12,
	}); err != nil {
		t.Fatalf("label move failed: %v", err)
	}

	got, ok := a.LabelVersion(alice, historyA1, "release")
	if !ok || got != versionA12 {
		t.Fatalf("label was not moved: got %q, exists=%v", got, ok)
	}
}

func TestLabelSetCreatesMissingLabel(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelSet, Name: "candidate", VersionHref: versionA12,
	}); err != nil {
		t.Fatalf("label set should create a missing label: %v", err)
	}
	got, ok := a.LabelVersion(alice, historyA1, "candidate")
	if !ok || got != versionA12 {
		t.Fatalf("label set result: got %q, exists=%v", got, ok)
	}
}

func TestLabelRemoveMissingFails(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelRemove, Name: "never-created",
	})
	requireError(t, err, ErrLabelNotFound)

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	}); err != nil {
		t.Fatalf("label add failed: %v", err)
	}
	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelRemove, Name: "release",
	}); err != nil {
		t.Fatalf("label remove failed: %v", err)
	}
	if _, ok := a.LabelVersion(alice, historyA1, "release"); ok {
		t.Fatal("label still exists after remove")
	}
}

func TestLabelsAreScopedToVersionHistory(t *testing.T) {
	a := newTestAuthorizer(t)
	for _, history := range []string{historyA1, historyA2} {
		if err := a.GrantHistoryWrite(alice, history); err != nil {
			t.Fatalf("grant setup failed: %v", err)
		}
	}

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	}); err != nil {
		t.Fatalf("label add in first history failed: %v", err)
	}
	if err := a.AuthorizeLabel(alice, historyA2, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA21,
	}); err != nil {
		t.Fatalf("same label name in another history should be independent: %v", err)
	}

	first, _ := a.LabelVersion(alice, historyA1, "release")
	second, _ := a.LabelVersion(alice, historyA2, "release")
	if first != versionA11 || second != versionA21 {
		t.Fatalf("history-scoped labels got first=%q second=%q", first, second)
	}
}

func TestLabelSetCannotUseVersionFromAnotherHistory(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelSet, Name: "release", VersionHref: versionA21,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want not found for a foreign-history target", err)
	}
	if _, ok := a.LabelVersion(alice, historyA1, "release"); ok {
		t.Fatal("failed set created a label")
	}
}

func TestLabelRequiresWriteAccess(t *testing.T) {
	a := newTestAuthorizer(t)

	err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	})
	requireError(t, err, ErrForbidden)

	// A caller from another tenant receives the same answer as a history
	// that does not exist.
	crossTenant := a.AuthorizeLabel(mallory, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	})
	requireError(t, crossTenant, ErrNotFound)
	if _, ok := a.LabelVersion(alice, historyA1, "release"); ok {
		t.Fatal("unauthorized label add mutated the history")
	}
}

func TestLabelCrossTenantTargetIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionB11,
	})
	requireError(t, err, ErrNotFound)
}

func TestLabelVersionRequiresHistoryWrite(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}
	if err := a.AuthorizeLabel(alice, historyA1, LabelChange{
		Action: LabelAdd, Name: "release", VersionHref: versionA11,
	}); err != nil {
		t.Fatalf("label add failed: %v", err)
	}

	if _, ok := a.LabelVersion(bob, historyA1, "release"); ok {
		t.Fatal("caller without history write access can read the label")
	}
	if _, ok := a.LabelVersion(mallory, historyA1, "release"); ok {
		t.Fatal("caller from another tenant can read the label")
	}
}

func TestLabelConcurrentAddHasSingleWinner(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	const contenders = 24
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- a.AuthorizeLabel(alice, historyA1, LabelChange{
				Action: LabelAdd, Name: "release", VersionHref: versionA11,
			})
		}()
	}
	wg.Wait()
	close(results)

	winners := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrLabelExists):
		default:
			t.Fatalf("unexpected concurrent add result: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("got %d label add winners, want exactly 1", winners)
	}
}

func TestCheckinWithoutCheckoutFails(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}

	err := a.AuthorizeCheckin(alice, workingA1)
	requireError(t, err, ErrCheckoutRequired)
}

func TestCheckinRequiresTheCheckingOutCaller(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}
	if err := a.Checkout(alice, workingA1); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	err := a.AuthorizeCheckin(bob, workingA1)
	requireError(t, err, ErrCheckoutRequired)

	// Bob's failed attempt must not disturb Alice's checkout.
	if err := a.AuthorizeCheckin(alice, workingA1); err != nil {
		t.Fatalf("owner CHECKIN denied after another caller's attempt: %v", err)
	}
}

func TestCheckinConsumesCheckout(t *testing.T) {
	a := newTestAuthorizer(t)
	if err := a.GrantHistoryWrite(alice, historyA1); err != nil {
		t.Fatalf("grant setup failed: %v", err)
	}
	if err := a.Checkout(alice, workingA1); err != nil {
		t.Fatalf("checkout failed: %v", err)
	}

	if err := a.AuthorizeCheckin(alice, workingA1); err != nil {
		t.Fatalf("legal CHECKIN denied: %v", err)
	}
	err := a.AuthorizeCheckin(alice, workingA1)
	requireError(t, err, ErrCheckoutRequired)
}

func TestCheckoutRequiresWriteAccess(t *testing.T) {
	a := newTestAuthorizer(t)

	err := a.Checkout(alice, workingA1)
	requireError(t, err, ErrForbidden)
}

func TestSecondCallerCannotCheckout(t *testing.T) {
	a := newTestAuthorizer(t)
	for _, principal := range []Principal{alice, bob} {
		if err := a.GrantHistoryWrite(principal, historyA1); err != nil {
			t.Fatalf("grant setup failed: %v", err)
		}
	}

	if err := a.Checkout(alice, workingA1); err != nil {
		t.Fatalf("first checkout failed: %v", err)
	}
	err := a.Checkout(bob, workingA1)
	requireError(t, err, ErrAlreadyCheckedOut)
}

func TestCheckinUnknownWorkingResourceIsNotFound(t *testing.T) {
	a := newTestAuthorizer(t)

	err := a.AuthorizeCheckin(alice, "/dav/tenants/a/working/missing")
	requireError(t, err, ErrNotFound)
}

func TestHrefWithQueryOrFragmentIsRejected(t *testing.T) {
	a := newTestAuthorizer(t)

	if err := a.GrantVersionRead(alice, versionA11+"?download=1"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("query href grant: got %v, want invalid request", err)
	}
	updateErr := a.AuthorizeUpdate(alice, resourceA1, versionA12+"#fragment")
	requireError(t, updateErr, ErrNotFound)
}

func TestHistoryHrefCannotAliasVersion(t *testing.T) {
	a := newTestAuthorizer(t)

	err := a.AddHistory(History{Href: versionA11, Tenant: tenantB})
	requireError(t, err, ErrAlreadyExists)
}
