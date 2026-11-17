package netsec_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0292"
)

func TestDirectoryGrantDoesNotAuthorizeReferencedSecret(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "dir:child"}},
		},
		"dir:child": {
			References: []netsec.Reference{{Target: "secret:db"}},
		},
		"secret:db": {},
	}
	permissions := allow("dir:root", "dir:child")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")

	if plan != nil {
		t.Fatal("PlanRestore returned a plan despite an unauthorized required secret")
	}
	if !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("PlanRestore error = %v, want ErrUnauthorized", err)
	}

	var unauthorized *netsec.UnauthorizedError
	if !errors.As(err, &unauthorized) {
		t.Fatalf("PlanRestore error = %T, want *UnauthorizedError", err)
	}
	if unauthorized.Object != "secret:db" {
		t.Fatalf("unauthorized object = %q, want secret:db", unauthorized.Object)
	}
	if want := []netsec.ObjectID{"dir:root", "dir:child", "secret:db"}; !reflect.DeepEqual(unauthorized.Path, want) {
		t.Fatalf("unauthorized path = %v, want %v", unauthorized.Path, want)
	}
}

func TestZeroValueReferenceIsRequired(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "secret:db"}},
		},
		"secret:db": {},
	}
	permissions := allow("dir:root")

	if _, err := netsec.PlanRestore(graph, permissions, "dir:root"); !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("PlanRestore error = %v, want ErrUnauthorized", err)
	}
}

func TestOptionalReferencesAreExplicitlyOmitted(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{
				{Target: "dir:child"},
				{Target: "secret:preview", Optional: true},
				{Target: "secret:locked", Optional: true},
				{Target: "secret:missing", Optional: true},
			},
		},
		"dir:child":      {},
		"secret:preview": {},
		"secret:locked":  {},
	}
	permissions := allow("dir:root", "dir:child", "secret:preview")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	if got, want := plan.Objects(), []netsec.ObjectID{"dir:child", "dir:root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Objects() = %v, want %v", got, want)
	}
	wantOmissions := []netsec.OmittedReference{
		{From: "dir:root", To: "secret:locked"},
		{From: "dir:root", To: "secret:missing"},
		{From: "dir:root", To: "secret:preview"},
	}
	if got := plan.OmittedOptional(); !reflect.DeepEqual(got, wantOmissions) {
		t.Fatalf("OmittedOptional() = %v, want %v", got, wantOmissions)
	}

	manifest, err := plan.Publish(permissions)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if manifest.Contains("secret:preview") || manifest.Contains("secret:locked") || manifest.Contains("secret:missing") {
		t.Fatalf("manifest silently included an optional object: %v", manifest.Objects())
	}
	if got := manifest.OmittedOptional(); !reflect.DeepEqual(got, wantOmissions) {
		t.Fatalf("manifest omissions = %v, want %v", got, wantOmissions)
	}
}

func TestCircularReferencesAreVisitedOnce(t *testing.T) {
	graph := netsec.Graph{
		"dir:a": {
			References: []netsec.Reference{{Target: "dir:b"}},
		},
		"dir:b": {
			References: []netsec.Reference{
				{Target: "dir:a"},
				{Target: "secret:shared"},
			},
		},
		"secret:shared": {},
	}
	permissions := allow("dir:a", "dir:b", "secret:shared")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:a")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}
	want := []netsec.ObjectID{"dir:a", "dir:b", "secret:shared"}
	if got := plan.Objects(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Objects() = %v, want %v", got, want)
	}

	manifest, err := plan.Publish(permissions)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if got := manifest.Objects(); !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest Objects() = %v, want %v", got, want)
	}
}

func TestSharedSecretIsAuthorizedIndividuallyAndRestoredOnce(t *testing.T) {
	graph := netsec.Graph{
		"dir:a": {
			References: []netsec.Reference{{Target: "secret:shared"}},
		},
		"dir:b": {
			References: []netsec.Reference{{Target: "secret:shared"}},
		},
		"secret:shared": {},
	}

	t.Run("denied shared secret rejects every root", func(t *testing.T) {
		permissions := allow("dir:a", "dir:b")

		plan, err := netsec.PlanRestore(graph, permissions, "dir:a", "dir:b")
		if plan != nil {
			t.Fatal("PlanRestore returned a plan with an unauthorized shared secret")
		}
		if !errors.Is(err, netsec.ErrUnauthorized) {
			t.Fatalf("PlanRestore error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("authorized shared secret appears once", func(t *testing.T) {
		permissions := allow("dir:a", "dir:b", "secret:shared")

		plan, err := netsec.PlanRestore(graph, permissions, "dir:a", "dir:b")
		if err != nil {
			t.Fatalf("PlanRestore returned error: %v", err)
		}
		want := []netsec.ObjectID{"dir:a", "dir:b", "secret:shared"}
		if got := plan.Objects(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Objects() = %v, want %v", got, want)
		}
	})
}

func TestPublishRejectsRevocationBetweenPlanAndPublish(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "secret:db"}},
		},
		"secret:db": {},
	}
	permissions := allow("dir:root", "secret:db")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	permissions.Revoke("secret:db")

	if err := plan.Review(permissions); !errors.Is(err, netsec.ErrPermissionChanged) || !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("Review error = %v, want permission-change and unauthorized errors", err)
	}

	manifest, err := plan.Publish(permissions)
	if manifest != nil {
		t.Fatal("Publish returned a manifest after a revocation")
	}
	if !errors.Is(err, netsec.ErrPermissionChanged) || !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("Publish error = %v, want permission-change and unauthorized errors", err)
	}

	var revoked *netsec.RevokedError
	if !errors.As(err, &revoked) {
		t.Fatalf("Publish error = %T, want *RevokedError", err)
	}
	if revoked.Object != "secret:db" {
		t.Fatalf("revoked object = %q, want secret:db", revoked.Object)
	}
}

func TestPublishIgnoresPermissionsOutsidePlannedClosure(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{
				{Target: "secret:optional", Optional: true},
			},
		},
		"secret:optional":  {},
		"secret:unrelated": {},
	}
	permissions := allow("dir:root", "secret:optional", "secret:unrelated")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	permissions.Revoke("secret:optional")
	permissions.Revoke("secret:unrelated")

	manifest, err := plan.Publish(permissions)
	if err != nil {
		t.Fatalf("Publish returned error for changes outside the planned closure: %v", err)
	}
	if manifest.Contains("secret:optional") || manifest.Contains("secret:unrelated") {
		t.Fatalf("manifest included an object outside the planned closure: %v", manifest.Objects())
	}
}

func TestPublishRejectsRevokeAndRegrant(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "secret:shared"}},
		},
		"secret:shared": {},
	}
	permissions := allow("dir:root", "secret:shared")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	permissions.Revoke("secret:shared")
	permissions.Grant("secret:shared")

	_, err = plan.Publish(permissions)
	if !errors.Is(err, netsec.ErrPermissionChanged) {
		t.Fatalf("Publish error = %v, want ErrPermissionChanged", err)
	}
	if errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("Publish error = %v, want a revision change rather than a current denial", err)
	}

	replanned, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("replan after regrant returned error: %v", err)
	}
	if _, err := replanned.Publish(permissions); err != nil {
		t.Fatalf("replanned Publish returned error: %v", err)
	}
}

func TestFinalReviewDetectsSharedSecretRevocationThroughCycle(t *testing.T) {
	graph := netsec.Graph{
		"dir:a": {
			References: []netsec.Reference{{Target: "dir:b"}},
		},
		"dir:b": {
			References: []netsec.Reference{
				{Target: "dir:a"},
				{Target: "secret:shared"},
			},
		},
		"dir:c": {
			References: []netsec.Reference{{Target: "secret:shared"}},
		},
		"secret:shared": {},
	}
	permissions := allow("dir:a", "dir:b", "dir:c", "secret:shared")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:a", "dir:c")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}
	permissions.Revoke("secret:shared")

	manifest, err := plan.Publish(permissions)
	if manifest != nil {
		t.Fatalf("Publish returned manifest %v after shared secret was revoked", manifest.Objects())
	}
	if !errors.Is(err, netsec.ErrPermissionChanged) || !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("Publish error = %v, want permission-change and unauthorized errors", err)
	}
}

func TestMissingRequiredObjectRejectsPlan(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "secret:missing"}},
		},
	}
	permissions := allow("dir:root")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if plan != nil {
		t.Fatal("PlanRestore returned a plan with a missing required object")
	}
	if !errors.Is(err, netsec.ErrMissingObject) {
		t.Fatalf("PlanRestore error = %v, want ErrMissingObject", err)
	}
}

func TestPlanRequiresAtLeastOneRoot(t *testing.T) {
	if _, err := netsec.PlanRestore(netsec.Graph{}, netsec.Permissions{}); !errors.Is(err, netsec.ErrInvalidRequest) {
		t.Fatalf("PlanRestore error = %v, want ErrInvalidRequest", err)
	}
}

func TestMissingPermissionIsDenied(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {},
	}

	if _, err := netsec.PlanRestore(graph, nil, "dir:root"); !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("PlanRestore error = %v, want ErrUnauthorized", err)
	}
}

func TestPublishFailsClosedWithNilPermissions(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {},
	}
	permissions := allow("dir:root")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	manifest, err := plan.Publish(nil)
	if manifest != nil {
		t.Fatal("Publish returned a manifest with nil permissions")
	}
	if !errors.Is(err, netsec.ErrUnauthorized) {
		t.Fatalf("Publish error = %v, want ErrUnauthorized", err)
	}
}

func TestPlanCopiesGraphAndPermissionSnapshot(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{{Target: "secret:db"}},
		},
		"secret:db": {},
	}
	permissions := allow("dir:root", "secret:db")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	graph["dir:root"] = netsec.Object{
		References: []netsec.Reference{{Target: "secret:other"}},
	}
	permissions.Revoke("secret:db")

	if got, want := plan.Objects(), []netsec.ObjectID{"dir:root", "secret:db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Objects() changed after inputs were mutated: got %v, want %v", got, want)
	}

	restoredSnapshot := allow("dir:root", "secret:db")
	if _, err := plan.Publish(restoredSnapshot); err != nil {
		t.Fatalf("Publish against the plan snapshot returned error: %v", err)
	}
}

func TestPublishOnlyOnce(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {},
	}
	permissions := allow("dir:root")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}
	if _, err := plan.Publish(permissions); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	if _, err := plan.Publish(permissions); !errors.Is(err, netsec.ErrAlreadyPublished) {
		t.Fatalf("second Publish error = %v, want ErrAlreadyPublished", err)
	}
}

func TestReturnedSlicesAreCopies(t *testing.T) {
	graph := netsec.Graph{
		"dir:root": {
			References: []netsec.Reference{
				{Target: "secret:optional", Optional: true},
			},
		},
	}
	permissions := allow("dir:root")

	plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}

	objects := plan.Objects()
	objects[0] = "dir:mutated"
	omissions := plan.OmittedOptional()
	omissions[0].To = "secret:mutated"

	if got, want := plan.Objects(), []netsec.ObjectID{"dir:root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Objects() was mutated through its return value: got %v, want %v", got, want)
	}
	if got := plan.OmittedOptional()[0].To; got != "secret:optional" {
		t.Fatalf("OmittedOptional() was mutated through its return value: target %q", got)
	}
}

func TestDeepRequiredChain(t *testing.T) {
	const depth = 50_000

	graph := make(netsec.Graph, depth)
	permissions := make(netsec.Permissions, depth)
	for i := 0; i < depth; i++ {
		id := netsec.ObjectID(fmt.Sprintf("obj:%d", i))
		permissions.Grant(id)
		if i+1 == depth {
			graph[id] = netsec.Object{}
			continue
		}
		next := netsec.ObjectID(fmt.Sprintf("obj:%d", i+1))
		graph[id] = netsec.Object{References: []netsec.Reference{{Target: next}}}
	}

	plan, err := netsec.PlanRestore(graph, permissions, "obj:0")
	if err != nil {
		t.Fatalf("PlanRestore returned error: %v", err)
	}
	if got := len(plan.Objects()); got != depth {
		t.Fatalf("Objects() length = %d, want %d", got, depth)
	}
	if _, err := plan.Publish(permissions); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
}

func allow(ids ...netsec.ObjectID) netsec.Permissions {
	permissions := make(netsec.Permissions, len(ids))
	for _, id := range ids {
		permissions.Grant(id)
	}
	return permissions
}
