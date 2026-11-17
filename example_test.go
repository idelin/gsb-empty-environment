package netsec

import (
	"errors"
	"fmt"
)

func ExampleTemplateStore() {
	store := NewTemplateStore()
	tmpl := baseTemplate()
	if err := store.Create(tmpl); err != nil {
		fmt.Println("create:", err)
		return
	}

	// Approval freezes the normalized template, image digest, identity and
	// security options; only replicas and labels may change on instances.
	approval, err := store.ApproveTemplate("billing-worker", "ap-1",
		[]string{"/spec/replicas", "/spec/labels"})
	if err != nil {
		fmt.Println("approve:", err)
		return
	}
	fmt.Println("bound digest:", approval.ImageDigest)

	// An allowed non-security change cites the mutable allow-list.
	instance, err := store.Derive("billing-worker", "ap-1", "inst-a",
		[]PatchOperation{{Op: "replace", Path: "/spec/replicas", Value: float64(12)}})
	if err != nil {
		fmt.Println("derive:", err)
		return
	}
	for _, decision := range instance.Decisions {
		fmt.Printf("allowed: %s %s -> %s\n", decision.Path, decision.Verdict, decision.Basis)
	}

	// A field move that touches a frozen identity is denied with the binding
	// cited as the authority.
	_, err = store.Derive("billing-worker", "ap-1", "inst-b",
		[]PatchOperation{{Op: "move", From: "/spec/labels/tier",
			Path: "/spec/identity/serviceAccountName"}})
	fmt.Println("identity move denied:", errors.Is(err, errDenied))

	// A post-approval template update invalidates the approval even if the
	// update itself succeeds under optimistic concurrency.
	updated := baseTemplate()
	updated.Spec.Identity.ServiceAccountName = "post-approval-sa"
	if _, err := store.Update("billing-worker", 1, updated); err != nil {
		fmt.Println("update:", err)
		return
	}
	_, err = store.Derive("billing-worker", "ap-1", "inst-c", nil)
	fmt.Println("stale approval rejected:", errors.Is(err, errStaleApproval))

	// Output:
	// bound digest: sha256:9c2b
	// allowed: /spec/replicas ALLOW -> MUTABLE_FIELD_ALLOWLIST
	// identity move denied: true
	// stale approval rejected: true
}
