package netsec

import "testing"

func TestDecodeKustomization(t *testing.T) {
	raw := map[string]any{
		"resources": []any{"base", "https://github.com/o/r?ref=" + pinnedCommit},
		"bases":     []any{"legacy-base"},
		"patches": []any{
			"legacy-string-patch.yaml",
			map[string]any{
				"path":  "strategic.yaml",
				"patch": "- op: replace",
			},
		},
		"generators": []any{
			"plugin-config.yaml",
			map[string]any{
				"apiVersion": "builtin",
				"kind":       "ConfigMapGenerator",
				"exec":       map[string]any{"command": "sh"},
			},
		},
		"transformers": []any{
			map[string]any{"kind": "ReplicaCountTransformer"},
		},
		"helmCharts": []any{
			map[string]any{
				"name":    "redis",
				"repo":    "https://charts.example.com",
				"version": "1.0.0",
			},
		},
		"configMapGenerator": []any{
			map[string]any{
				"name":  "cm",
				"env":   "env.txt",
				"files": []any{"a.txt", "b=path/b.txt"},
			},
		},
	}

	kustomization, err := DecodeKustomization(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(kustomization.Resources) != 2 || kustomization.Resources[0] != "base" {
		t.Fatalf("resources decoded incorrectly: %+v", kustomization.Resources)
	}
	if len(kustomization.Bases) != 1 || kustomization.Bases[0] != "legacy-base" {
		t.Fatalf("bases decoded incorrectly: %+v", kustomization.Bases)
	}
	if len(kustomization.Patches) != 2 || kustomization.Patches[0].Path != "legacy-string-patch.yaml" {
		t.Fatalf("patches decoded incorrectly: %+v", kustomization.Patches)
	}
	if kustomization.Patches[1].Path != "strategic.yaml" || kustomization.Patches[1].Patch == "" {
		t.Fatalf("structured patch decoded incorrectly: %+v", kustomization.Patches[1])
	}
	if len(kustomization.Generators) != 2 {
		t.Fatalf("generators decoded incorrectly: %+v", kustomization.Generators)
	}
	if kustomization.Generators[0].Path != "plugin-config.yaml" {
		t.Fatalf("string generator should decode to a path: %+v", kustomization.Generators[0])
	}
	if kustomization.Generators[1].Spec == nil || len(kustomization.Generators[1].RawSpec) == 0 {
		t.Fatalf("inline generator should retain spec and raw bytes")
	}
	if kustomization.HelmCharts[0].Version != "1.0.0" {
		t.Fatalf("helm chart decoded incorrectly: %+v", kustomization.HelmCharts[0])
	}
	if kustomization.ConfigMapGenerators[0].Files[1] != "b=path/b.txt" {
		t.Fatalf("generator files decoded incorrectly: %+v", kustomization.ConfigMapGenerators[0])
	}

	admitter := NewAdmitter(testCatalog(t, nil), []string{"github.com", "charts.example.com"})
	if err := admitter.Validate(kustomization); err == nil {
		t.Fatal("decoded inline exec generator must be rejected by Validate")
	} else {
		assertViolation(t, err, ReasonExecPlugin)
	}
}

func TestDecodeKustomizationTypeErrors(t *testing.T) {
	if _, err := DecodeKustomization(map[string]any{
		"resources": "not-a-list",
	}); err == nil {
		t.Fatal("expected type error for resources")
	}
	if _, err := DecodeKustomization(map[string]any{
		"patches": []any{42},
	}); err == nil {
		t.Fatal("expected type error for patch entry")
	}
	if _, err := DecodeKustomization(nil); err == nil {
		t.Fatal("expected error for nil mapping")
	}
}
