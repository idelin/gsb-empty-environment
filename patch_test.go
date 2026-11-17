package netsec

import (
	"errors"
	"testing"
)

func sampleTree() map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"name": "billing-worker",
			"labels": map[string]any{
				"team": "billing",
			},
		},
		"spec": map[string]any{
			"replicas": float64(3),
			"env": []any{
				map[string]any{"name": "A", "value": "1"},
				map[string]any{"name": "B", "value": "2"},
			},
		},
	}
}

func TestApplyPatchAddReplaceRemove(t *testing.T) {
	out, _, err := ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "add", Path: "/spec/replicas", Value: float64(5)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["spec"].(map[string]any)["replicas"]; got != float64(5) {
		t.Fatalf("add/replace failed: %v", got)
	}

	out, _, err = ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "remove", Path: "/metadata/labels/team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := out["metadata"].(map[string]any)["labels"].(map[string]any)
	if _, ok := labels["team"]; ok {
		t.Fatal("remove did not delete the key")
	}
}

func TestApplyPatchMoveAndCopy(t *testing.T) {
	out, records, err := ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "move", From: "/metadata/labels/team", Path: "/spec/labels/team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := out["metadata"].(map[string]any)["labels"].(map[string]any)
	if _, ok := source["team"]; ok {
		t.Fatal("move left the source in place")
	}
	dest, ok := out["spec"].(map[string]any)["labels"].(map[string]any)["team"]
	if !ok || dest != "billing" {
		t.Fatalf("move destination missing: %+v", out)
	}
	if records[0].Op != "move" || records[0].From != "/metadata/labels/team" {
		t.Fatalf("move record wrong: %+v", records[0])
	}

	out, _, err = ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "copy", From: "/metadata/name", Path: "/spec/displayName"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["spec"].(map[string]any)["displayName"] != "billing-worker" {
		t.Fatal("copy did not duplicate the value")
	}
}

func TestApplyPatchArrayInsertion(t *testing.T) {
	out, _, err := ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "add", Path: "/spec/env/-", Value: map[string]any{"name": "C", "value": "3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := out["spec"].(map[string]any)["env"].([]any)
	if len(env) != 3 {
		t.Fatalf("array insertion failed: %d", len(env))
	}

	out, _, err = ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "remove", Path: "/spec/env/0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env = out["spec"].(map[string]any)["env"].([]any)
	if len(env) != 1 || env[0].(map[string]any)["name"] != "B" {
		t.Fatalf("array removal failed: %+v", env)
	}
}

func TestApplyPatchRejectsBadPointer(t *testing.T) {
	_, _, err := ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "remove", Path: "spec/replicas"},
	})
	if !errors.Is(err, errPointer) {
		t.Fatalf("expected errPointer, got %v", err)
	}

	_, _, err = ApplyPatch(sampleTree(), []PatchOperation{
		{Op: "remove", Path: "/spec/env/9"},
	})
	if !errors.Is(err, errPatch) {
		t.Fatalf("expected wrapped errPatch, got %v", err)
	}
}

func TestStructuralDiffDetectsAddRemoveReplace(t *testing.T) {
	before := map[string]any{
		"spec": map[string]any{"replicas": float64(1), "image": "a@sha256:1"},
	}
	after := map[string]any{
		"spec": map[string]any{"replicas": float64(2), "name": "new"},
	}
	changes := structuralDiff(before, after, "")
	seen := map[string]string{}
	for _, change := range changes {
		seen[change.Path] = change.Op
	}
	if seen["/spec/replicas"] != "replace" ||
		seen["/spec/image"] != "remove" ||
		seen["/spec/name"] != "add" {
		t.Fatalf("unexpected diff: %+v", seen)
	}
}
