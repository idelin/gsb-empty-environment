package netsec

import "testing"

func TestLocalPathTraversalRejected(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	cases := []struct {
		name   string
		config *Kustomization
		field  string
		reason Reason
	}{
		{
			name: "resource parent escape",
			config: &Kustomization{
				Resources: []string{"../secrets/tls.yaml"},
			},
			field:  "resources[0]",
			reason: ReasonPathEscapesRoot,
		},
		{
			name: "base parent escape",
			config: &Kustomization{
				Bases: []string{"../../etc/kustomize"},
			},
			field:  "bases[0]",
			reason: ReasonPathEscapesRoot,
		},
		{
			name: "patch parent escape",
			config: &Kustomization{
				Patches: []Patch{{Path: "overlays/../../../etc/passwd"}},
			},
			field:  "patches[0]",
			reason: ReasonPathEscapesRoot,
		},
		{
			name: "config map file parent escape",
			config: &Kustomization{
				ConfigMapGenerators: []GeneratorArgs{{
					Name:  "cm",
					Files: []string{"key=../outside.env"},
				}},
			},
			field:  "configMapGenerator",
			reason: ReasonPathEscapesRoot,
		},
		{
			name: "absolute path rejected",
			config: &Kustomization{
				Resources: []string{"/etc/shadow"},
			},
			field:  "resources[0]",
			reason: ReasonAbsolutePath,
		},
		{
			name: "empty path rejected",
			config: &Kustomization{
				Resources: []string{""},
			},
			field:  "resources[0]",
			reason: ReasonEmptyPath,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := admitter.Validate(tc.config)
			assertViolation(t, err, tc.reason)
			var admissionErr *AdmissionError
			_ = asAdmissionError(err, &admissionErr)
			violation := admissionErr.Violations[0]
			if fieldPrefix(violation.Field) != fieldPrefix(tc.field) {
				t.Fatalf("expected violation on %s, got %q", tc.field, violation.Field)
			}
		})
	}
}

func asAdmissionError(err error, target **AdmissionError) bool {
	if admissionErr, ok := err.(*AdmissionError); ok {
		*target = admissionErr
		return true
	}
	return false
}

func TestContainedPathsAllowed(t *testing.T) {
	files := []FileInfo{
		{RepoPath: "overlays/dev/kustomization.yaml"},
		{RepoPath: "overlays/base/deploy.yaml"},
		{RepoPath: "config/app.yaml"},
	}
	admitter := NewAdmitter(testCatalog(t, files), nil)

	err := admitter.Validate(&Kustomization{
		Resources: []string{
			"overlays/base/deploy.yaml",
			"./config/app.yaml",
			"overlays/dev/../base/deploy.yaml",
		},
		Patches: []Patch{{Path: "config/app.yaml"}},
		ConfigMapGenerators: []GeneratorArgs{{
			Name:  "cm",
			Env:   "config/app.yaml",
			Files: []string{"config/app.yaml", "alias=config/app.yaml"},
		}},
	})
	assertAllowed(t, err)
}

func TestSymlinkEscapeAndCycleRejected(t *testing.T) {
	files := []FileInfo{
		{RepoPath: "overlays/dev/evil.yaml", IsSymlink: true, LinkTarget: "../../../etc/passwd"},
		{RepoPath: "overlays/dev/loop-a", IsSymlink: true, LinkTarget: "loop-b"},
		{RepoPath: "overlays/dev/loop-b", IsSymlink: true, LinkTarget: "loop-a"},
		{RepoPath: "overlays/dev/outside", IsSymlink: true, LinkTarget: "/absolute/outside"},
		{RepoPath: "overlays/dev/inside.yaml", IsSymlink: true, LinkTarget: "../base/inside.yaml"},
		{RepoPath: "overlays/base/inside.yaml"},
		{RepoPath: "config/app.yaml"},
	}
	admitter := NewAdmitter(testCatalog(t, files), nil)

	t.Run("symlink pointing outside root", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"overlays/dev/evil.yaml"},
		})
		assertViolation(t, err, ReasonSymlinkEscapesRoot)
	})

	t.Run("absolute symlink outside root", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"overlays/dev/outside"},
		})
		assertViolation(t, err, ReasonSymlinkEscapesRoot)
	})

	t.Run("symlink cycle", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"overlays/dev/loop-a"},
		})
		assertViolation(t, err, ReasonSymlinkCycle)
	})

	t.Run("symlink staying inside root is allowed", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources:    []string{"overlays/dev/inside.yaml", "config/app.yaml"},
			Generators:   []PluginEntry{{Path: "overlays/dev/inside.yaml"}},
			Transformers: []PluginEntry{{Path: "config/app.yaml"}},
		})
		assertAllowed(t, err)
	})
}

func TestSymlinkPrefixResolution(t *testing.T) {
	files := []FileInfo{
		{RepoPath: "linkdir", IsSymlink: true, LinkTarget: "real/dir"},
		{RepoPath: "real/dir/app.yaml"},
	}
	catalog := testCatalog(t, files)

	resolved, reason, _ := resolveLocalPath(catalog, "", "linkdir/app.yaml")
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if resolved != "real/dir/app.yaml" {
		t.Fatalf("expected prefix resolution, got %q", resolved)
	}

	escapeFiles := []FileInfo{
		{RepoPath: "escape", IsSymlink: true, LinkTarget: "../../outside"},
	}
	escapeCatalog := testCatalog(t, escapeFiles)
	_, reason, _ = resolveLocalPath(escapeCatalog, "", "escape/deep/file.yaml")
	if reason != ReasonSymlinkEscapesRoot {
		t.Fatalf("expected symlink escape, got %q", reason)
	}
}

func TestFileCatalogRejectsOutOfRootEntries(t *testing.T) {
	if _, err := NewFileCatalog([]FileInfo{{RepoPath: "../outside.yaml"}}); err == nil {
		t.Fatal("expected catalog construction to reject out-of-root entry")
	}
	if _, err := NewFileCatalog([]FileInfo{{RepoPath: ""}}); err == nil {
		t.Fatal("expected catalog construction to reject empty entry")
	}
}
