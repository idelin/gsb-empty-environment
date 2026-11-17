package netsec

import (
	"errors"
	"sync"
	"testing"
)

func newApprovedStore(t *testing.T, mutable ...string) (*TemplateStore, *Approval) {
	t.Helper()
	store := NewTemplateStore()
	if err := store.Create(baseTemplate()); err != nil {
		t.Fatal(err)
	}
	approval, err := store.ApproveTemplate("billing-worker", "ap-1", mutable)
	if err != nil {
		t.Fatal(err)
	}
	return store, approval
}

func TestStoreDeriveAllowsApprovedMutation(t *testing.T) {
	store, _ := newApprovedStore(t, "/spec/replicas")
	instance, err := store.Derive("billing-worker", "ap-1", "inst-1", []PatchOperation{
		{Op: "replace", Path: "/spec/replicas", Value: float64(7)},
	})
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}
	if instance.Spec.Replicas == nil || *instance.Spec.Replicas != 7 {
		t.Fatalf("replicas not derived: %+v", instance.Spec.Replicas)
	}
	if instance.Spec.Identity.ServiceAccountName != "billing-sa" {
		t.Fatal("identity binding must be preserved")
	}
	if len(instance.Decisions) == 0 || instance.Decisions[0].Basis != BasisMutableAllowList {
		t.Fatalf("derivation must record an authorization basis: %+v", instance.Decisions)
	}
}

func TestStoreDeriveRejectsIdentityTampering(t *testing.T) {
	store, _ := newApprovedStore(t, "/spec/replicas")
	instance, err := store.Derive("billing-worker", "ap-1", "inst-2", []PatchOperation{
		{Op: "replace", Path: "/spec/identity/serviceAccountName", Value: "attacker-sa"},
	})
	if !errors.Is(err, errDenied) {
		t.Fatalf("expected errDenied, got %v", err)
	}
	if instance == nil || len(instance.Decisions) == 0 {
		t.Fatal("denied derivation must still return audit decisions")
	}
	if instance.Spec.Identity.ServiceAccountName == "attacker-sa" {
		t.Fatal("unapproved identity must never be bound to the returned instance spec")
	}
}

func TestStoreConcurrentUpdateInvalidatesApproval(t *testing.T) {
	store, _ := newApprovedStore(t, "/spec/replicas")

	_, current, err := store.Get("billing-worker")
	if err != nil {
		t.Fatal(err)
	}

	updated := baseTemplate()
	updated.Spec.Identity.ServiceAccountName = "post-approval-sa"
	newVersion, err := store.Update("billing-worker", current, updated)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if newVersion != current+1 {
		t.Fatalf("version not advanced: %d", newVersion)
	}

	_, err = store.Derive("billing-worker", "ap-1", "inst-3", nil)
	if !errors.Is(err, errStaleApproval) {
		t.Fatalf("stale approval must be rejected, got %v", err)
	}

	// Re-approving the new version restores derivation, and the new identity is
	// now the frozen one.
	if _, err := store.ApproveTemplate("billing-worker", "ap-2", []string{"/spec/replicas"}); err != nil {
		t.Fatal(err)
	}
	instance, err := store.Derive("billing-worker", "ap-2", "inst-4", nil)
	if err != nil {
		t.Fatalf("re-approval should restore derivation: %v", err)
	}
	if instance.Spec.Identity.ServiceAccountName != "post-approval-sa" {
		t.Fatalf("re-approved identity not bound: %s", instance.Spec.Identity.ServiceAccountName)
	}
}

func TestStoreOptimisticConcurrencyConflict(t *testing.T) {
	store, _ := newApprovedStore(t, "/spec/replicas")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tmpl := baseTemplate()
			tmpl.Spec.Replicas = intPtr(10 + i)
			_, err := store.Update("billing-worker", 1, tmpl)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	conflicts := 0
	for err := range errs {
		if errors.Is(err, errConflict) {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("exactly one concurrent update must conflict, got %d", conflicts)
	}
	version, err := store.Version("billing-worker")
	if err != nil || version != 2 {
		t.Fatalf("winning update should advance version to 2, got %d (%v)", version, err)
	}
}

func TestStoreMissingTemplateAndApproval(t *testing.T) {
	store := NewTemplateStore()
	if _, _, err := store.Get("missing"); !errors.Is(err, errNotFound) {
		t.Fatalf("expected errNotFound, got %v", err)
	}
	if err := store.Create(baseTemplate()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Derive("billing-worker", "no-such-approval", "inst", nil); !errors.Is(err, errApproval) {
		t.Fatalf("expected errApproval, got %v", err)
	}
}
