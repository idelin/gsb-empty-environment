package netsec

import (
	"errors"
	"testing"
)

// TestScenarioStructuredPatchAudit walks the three simulations called out in
// the task: a field move, deletion-with-default-restoration, and a concurrent
// template update — asserting the authority returned for each change.
func TestScenarioStructuredPatchAudit(t *testing.T) {
	store := NewTemplateStore()
	if err := store.Create(baseTemplate()); err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveTemplate("billing-worker", "ap-scenario",
		[]string{"/spec/replicas", "/spec/labels", "/metadata/labels"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("field move cites both endpoints", func(t *testing.T) {
		instance, err := store.Derive("billing-worker", "ap-scenario", "inst-move",
			[]PatchOperation{{
				Op:   "move",
				From: "/metadata/labels/team",
				Path: "/spec/labels/team",
			}})
		if err != nil {
			t.Fatalf("allow-listed move failed: %v", err)
		}
		if len(instance.Decisions) != 2 {
			t.Fatalf("expected a decision per changed endpoint, got %d", len(instance.Decisions))
		}
		for _, decision := range instance.Decisions {
			if decision.Basis != BasisMutableAllowList || decision.Verdict != VerdictAllow {
				t.Fatalf("move endpoint not authorized by allow-list: %+v", decision)
			}
		}
	})

	t.Run("security field move is denied with frozen basis", func(t *testing.T) {
		_, err := store.Derive("billing-worker", "ap-scenario", "inst-move-deny",
			[]PatchOperation{{
				Op:   "move",
				From: "/spec/labels/tier",
				Path: "/spec/identity/serviceAccountName",
			}})
		if !errors.Is(err, errDenied) {
			t.Fatalf("expected denial, got %v", err)
		}
	})

	t.Run("deletion restores the approved default value", func(t *testing.T) {
		instance, err := store.Derive("billing-worker", "ap-scenario", "inst-default",
			[]PatchOperation{{Op: "remove", Path: "/spec/security/runAsNonRoot"}})
		if err != nil {
			t.Fatalf("default-restoring removal failed: %v", err)
		}
		decision := instance.Decisions[0]
		if decision.Basis != BasisFrozenSecurity || decision.Verdict != VerdictAllow {
			t.Fatalf("removal must cite the frozen security binding: %+v", decision)
		}
		if instance.Spec.Security.RunAsNonRoot == nil ||
			!*instance.Spec.Security.RunAsNonRoot {
			t.Fatal("approved default was not restored")
		}
	})

	t.Run("concurrent template update invalidates the approval", func(t *testing.T) {
		_, version, err := store.Get("billing-worker")
		if err != nil {
			t.Fatal(err)
		}
		updated := baseTemplate()
		updated.Spec.Identity.ServiceAccountName = "post-approval-sa"
		if _, err := store.Update("billing-worker", version, updated); err != nil {
			t.Fatal(err)
		}

		// A stale version in a second update loses the optimistic-concurrency
		// race rather than silently overwriting the first update.
		if _, err := store.Update("billing-worker", version, updated); !errors.Is(err, errConflict) {
			t.Fatalf("expected errConflict, got %v", err)
		}

		_, err = store.Derive("billing-worker", "ap-scenario", "inst-stale",
			[]PatchOperation{{Op: "replace", Path: "/spec/replicas", Value: float64(1)}})
		if !errors.Is(err, errStaleApproval) {
			t.Fatalf("stale approval must be rejected, got %v", err)
		}

		reapproved, err := store.ApproveTemplate("billing-worker", "ap-scenario-2",
			approval.MutableFields)
		if err != nil {
			t.Fatal(err)
		}
		if reapproved.ImageDigest != approval.ImageDigest {
			t.Fatal("re-approval must keep the same digest binding")
		}
		instance, err := store.Derive("billing-worker", "ap-scenario-2", "inst-reapproved", nil)
		if err != nil {
			t.Fatalf("re-approved derivation failed: %v", err)
		}
		if instance.Spec.Identity.ServiceAccountName != "post-approval-sa" {
			t.Fatal("re-approval must bind the new identity")
		}
	})
}
