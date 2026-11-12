package netsec

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

var ctxbg = context.Background()

func dir(id ObjectID) Object { return Object{ID: id, Kind: KindDirectory} }
func sec(id ObjectID) Object { return Object{ID: id, Kind: KindSecret} }
func req(a, b ObjectID) Ref  { return Ref{From: a, To: b, Kind: EdgeRequired} }
func opt(a, b ObjectID) Ref  { return Ref{From: a, To: b, Kind: EdgeOptional} }

func ids(ss ...string) []ObjectID {
	out := make([]ObjectID, 0, len(ss))
	for _, s := range ss {
		out = append(out, ObjectID(s))
	}
	return out
}

// failingAuth always returns a transient backend error, modeling an
// unreachable permission service.
type failingAuth struct{}

func (failingAuth) Allowed(context.Context, ObjectID) (bool, string, error) {
	return false, "unreachable", errors.New("permission service unavailable")
}

func TestRequiredClosureIncludesTransitiveSecrets(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), dir("d2"), sec("s1"), sec("s2")},
		[]Ref{req("d1", "d2"), req("d2", "s1"), req("s1", "s2")},
	)
	auth := NewGrantTable("d1", "d2", "s1", "s2")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Status() != StatusReady {
		t.Fatalf("status = %q, want ready", plan.Status())
	}
	want := ids("d1", "d2", "s1", "s2")
	if got := plan.Restore(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restore = %v, want %v", got, want)
	}
	if len(plan.Missing()) != 0 {
		t.Fatalf("unexpected missing grants: %+v", plan.Missing())
	}
}

func TestUnauthorizedRequiredSecretRejectsWholeRecovery(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), dir("d2"), sec("s1")},
		[]Ref{req("d1", "d2"), req("d2", "s1")},
	)
	// Directory-level grant exists, but the referenced secret is not granted.
	auth := NewGrantTable("d1", "d2")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err == nil {
		t.Fatal("expected rejection, got nil error")
	}
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error %v does not wrap ErrPlanRejected", err)
	}
	if plan.Status() != StatusRejected {
		t.Fatalf("status = %q, want rejected", plan.Status())
	}
	if got := plan.Restore(); got != nil {
		t.Fatalf("rejected plan must not materialize anything, got %v", got)
	}
	missing := plan.Missing()
	if len(missing) != 1 || missing[0].ID != "s1" || missing[0].Source != "d2" {
		t.Fatalf("missing = %+v, want single s1 referenced by d2", missing)
	}
}

func TestUnknownObjectIsDeniedByDefault(t *testing.T) {
	g := NewGraph([]Object{dir("d1")}, nil)
	auth := NewGrantTable()

	_, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error = %v, want ErrPlanRejected", err)
	}
}

func TestDanglingRequiredReferenceRejects(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1")},
		[]Ref{req("d1", "ghost")},
	)
	auth := NewGrantTable("d1", "ghost")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error = %v, want ErrPlanRejected", err)
	}
	found := false
	for _, miss := range plan.Missing() {
		if miss.ID == "ghost" && miss.Reason != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing ghost, got %+v", plan.Missing())
	}
}

func TestOptionalDependencyLeftEmptyUnlessRequested(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), sec("opt-secret")},
		[]Ref{opt("d1", "opt-secret")},
	)
	auth := NewGrantTable("d1", "opt-secret")

	// Default: optional target is not silently pulled.
	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := plan.Restore(); !reflect.DeepEqual(got, ids("d1")) {
		t.Fatalf("restore = %v, want only d1", got)
	}
	holes := plan.Holes()
	if len(holes) != 1 || holes[0].To != "opt-secret" || holes[0].Status != HoleLeftEmpty {
		t.Fatalf("holes = %+v, want single left_empty opt-secret", holes)
	}

	// Explicit opt-in pulls it in and enforces authorization.
	plan2, err := PlanRestore(ctxbg, g, auth, Request{
		Roots:           ids("d1"),
		IncludeOptional: map[ObjectID]bool{"opt-secret": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := plan2.Restore(); !reflect.DeepEqual(got, ids("d1", "opt-secret")) {
		t.Fatalf("restore = %v, want d1 and opt-secret", got)
	}
}

func TestExplicitOptionalStillRequiresGrant(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), sec("opt-secret")},
		[]Ref{opt("d1", "opt-secret")},
	)
	// opt-secret exists but has no grant.
	auth := NewGrantTable("d1")

	plan, err := PlanRestore(ctxbg, g, auth, Request{
		Roots:           ids("d1"),
		IncludeOptional: map[ObjectID]bool{"opt-secret": true},
	})
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error = %v, want ErrPlanRejected", err)
	}
	if plan.Status() != StatusRejected {
		t.Fatalf("status = %q, want rejected", plan.Status())
	}
}

func TestOptionalTargetCoveredByRequiredPath(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), dir("d2"), sec("s1")},
		[]Ref{req("d1", "s1"), opt("d2", "s1")},
	)
	auth := NewGrantTable("d1", "d2", "s1")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1", "d2")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	holes := plan.Holes()
	if len(holes) != 1 || holes[0].Status != HoleCovered {
		t.Fatalf("holes = %+v, want one covered hole", holes)
	}
	// The shared secret appears exactly once in the restore set.
	restore := plan.Restore()
	count := 0
	for _, id := range restore {
		if id == "s1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("s1 appeared %d times in %v", count, restore)
	}
}

func TestCyclicReferencesTerminateAndRestore(t *testing.T) {
	g := NewGraph(
		[]Object{dir("a"), dir("b"), sec("k")},
		[]Ref{req("a", "b"), req("b", "a"), req("b", "k")},
	)
	auth := NewGrantTable("a", "b", "k")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("a")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ids("a", "b", "k")
	if got := plan.Restore(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restore = %v, want %v", got, want)
	}
}

func TestCycleWithUnauthorizedNodeRejects(t *testing.T) {
	g := NewGraph(
		[]Object{dir("a"), dir("b")},
		[]Ref{req("a", "b"), req("b", "a")},
	)
	// b is part of the cycle but is not granted.
	auth := NewGrantTable("a")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("a")})
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error = %v, want ErrPlanRejected", err)
	}
	if !slices.ContainsFunc(plan.Missing(), func(m MissingGrant) bool { return m.ID == "b" }) {
		t.Fatalf("expected b in missing, got %+v", plan.Missing())
	}
}

func TestSharedSecretRestoredOnceAcrossDirectories(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), dir("d2"), sec("shared")},
		[]Ref{req("d1", "shared"), req("d2", "shared")},
	)
	auth := NewGrantTable("d1", "d2", "shared")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1", "d2")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := plan.Restore()
	want := ids("d1", "d2", "shared")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restore = %v, want %v (shared secret must appear once)", got, want)
	}
}

func TestRevocationBetweenPlanAndPublishIsCaught(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), sec("s1")},
		[]Ref{req("d1", "s1")},
	)
	auth := NewGrantTable("d1", "s1")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err != nil {
		t.Fatalf("unexpected planning error: %v", err)
	}

	// Immediately after planning, the final verification passes.
	if err := plan.Reverify(ctxbg, auth); err != nil {
		t.Fatalf("reverify before revocation: %v", err)
	}

	// The secret's grant is revoked before the host publishes.
	auth.Revoke("s1")

	if err := plan.Reverify(ctxbg, auth); !errors.Is(err, ErrRevoked) {
		t.Fatalf("reverify after revocation error = %v, want ErrRevoked", err)
	}
	published, err := plan.Publish(ctxbg, auth)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("publish error = %v, want ErrRevoked", err)
	}
	if published != nil {
		t.Fatalf("revoked plan must not publish objects, got %v", published)
	}
}

func TestPublishSucceedsWhenGrantsHold(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), sec("s1")},
		[]Ref{req("d1", "s1")},
	)
	auth := NewGrantTable("d1", "s1")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err != nil {
		t.Fatalf("unexpected planning error: %v", err)
	}
	published, err := plan.Publish(ctxbg, auth)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if want := ids("d1", "s1"); !reflect.DeepEqual(published, want) {
		t.Fatalf("published = %v, want %v", published, want)
	}
}

func TestBackendFailureFailsClosed(t *testing.T) {
	g := NewGraph([]Object{dir("d1")}, nil)
	plan, err := PlanRestore(ctxbg, g, failingAuth{}, Request{Roots: ids("d1")})
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("error = %v, want ErrPlanRejected", err)
	}
	if plan.Status() != StatusRejected {
		t.Fatalf("status = %q, want rejected", plan.Status())
	}
}

func TestNoRootsRejected(t *testing.T) {
	g := NewGraph(nil, nil)
	if _, err := PlanRestore(ctxbg, g, NewGrantTable(), Request{}); !errors.Is(err, ErrNoRoots) {
		t.Fatalf("error = %v, want ErrNoRoots", err)
	}
}

func TestSnapshotFreezesPlanTimeReason(t *testing.T) {
	g := NewGraph(
		[]Object{dir("d1"), sec("s1")},
		[]Ref{req("d1", "s1")},
	)
	auth := NewGrantTable("d1", "s1")

	plan, err := PlanRestore(ctxbg, g, auth, Request{Roots: ids("d1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	snap := plan.Snapshot()
	if snap["s1"] != "grant present" {
		t.Fatalf("snapshot reason for s1 = %q", snap["s1"])
	}

	// Mutating the live table does not alter the frozen snapshot.
	auth.Revoke("s1")
	if got := plan.Snapshot()["s1"]; got != "grant present" {
		t.Fatalf("snapshot changed after revocation: %q", got)
	}
}
