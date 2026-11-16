package kustomization

import (
	"strings"
	"testing"
)

const pinnedCommit = "0123456789abcdef0123456789abcdef01234567"

func baseFiles() []FileEntry {
	return []FileEntry{
		{Path: "kustomization.yaml"},
		{Path: "base", IsDir: true},
		{Path: "base/kustomization.yaml"},
		{Path: "base/deploy.yaml"},
		{Path: "overlay", IsDir: true},
		{Path: "overlay/dev", IsDir: true},
		{Path: "overlay/dev/kustomization.yaml"},
		{Path: "overlay/dev/patch.yaml"},
		{Path: "config", IsDir: true},
		{Path: "config/app.properties"},
		{Path: "safe-link", IsSymlink: true, LinkTarget: "config/app.properties"},
		{Path: "evil-link", IsSymlink: true, LinkTarget: "../../etc/passwd"},
		{Path: "abs-evil", IsSymlink: true, LinkTarget: "/etc/passwd"},
	}
}

func validRequest() *Request {
	return &Request{
		Entry:  "kustomization.yaml",
		Config: &Kustomization{},
		Files:  baseFiles(),
		AllowedHosts: []string{
			"github.com",
			"charts.example.com",
		},
	}
}

func TestAdmitAllowsCleanKustomization(t *testing.T) {
	req := validRequest()
	req.Config.Resources = []any{
		"base",
		"https://github.com/org/deploy?ref=" + pinnedCommit,
	}
	req.Config.Patches = []any{
		"overlay/dev/patch.yaml",
		map[string]any{"path": "overlay/dev/patch.yaml"},
	}
	req.Config.Bases = []any{"base"}
	req.Config.Generators = []any{"overlay/dev/generator.yaml"}
	req.Config.Transformers = []any{
		map[string]any{
			"apiVersion": "builtin.example.com/v1",
			"kind":       "LabelTransformer",
			"metadata":   map[string]any{"name": "labels"},
		},
	}
	req.Config.HelmCharts = []HelmChart{{
		Name:    "redis",
		Version: "18.19.0",
		Repo:    "https://charts.example.com",
	}}
	req.Config.ConfigMapGenerator = []ConfigMapEntry{{
		Name:  "app-config",
		Files: []string{"config/app.properties", "safe-link"},
	}}
	req.Files = append(req.Files, FileEntry{Path: "overlay/dev/generator.yaml"})

	if err := Admit(req); err != nil {
		t.Fatalf("expected admission, got %v", err)
	}
}

func TestAdmitRejectsTraversal(t *testing.T) {
	tests := []struct {
		name  string
		field string
		build func(kc *Kustomization)
	}{
		{
			name:  "resource parent escape",
			field: "resources",
			build: func(kc *Kustomization) { kc.Resources = []any{"../outside"} },
		},
		{
			name:  "base parent escape",
			field: "bases",
			build: func(kc *Kustomization) { kc.Bases = []any{"../../outside"} },
		},
		{
			name:  "patch object parent escape",
			field: "patches",
			build: func(kc *Kustomization) {
				kc.Patches = []any{map[string]any{"path": "../patch.yaml"}}
			},
		},
		{
			name:  "absolute resource",
			field: "resources",
			build: func(kc *Kustomization) {
				kc.Resources = []any{"/etc/kustomize"}
			},
		},
		{
			name:  "configmap file escape",
			field: "configMapGenerator",
			build: func(kc *Kustomization) {
				kc.ConfigMapGenerator = []ConfigMapEntry{{
					Name:  "cm",
					Files: []string{"../secrets.properties"},
				}}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.build(req.Config)
			err := Admit(req)
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !hasViolation(err, tc.field, "repository root") &&
				!hasViolation(err, tc.field, "absolute") {
				t.Fatalf("expected a root-escape violation on %s, got %v", tc.field, err)
			}
		})
	}
}

func TestAdmitRejectsSymlinkOutsideRoot(t *testing.T) {
	req := validRequest()
	req.Config.Resources = []any{"evil-link"}
	err := Admit(req)
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if !hasViolation(err, "resources", "symlink") {
		t.Fatalf("expected symlink violation, got %v", err)
	}

	req = validRequest()
	req.RootDir = "/workspace/repo"
	req.Config.Resources = []any{"abs-evil"}
	err = Admit(req)
	if err == nil || !hasViolation(err, "resources", "outside the repository root") {
		t.Fatalf("expected absolute symlink escape rejection, got %v", err)
	}
}

func TestAdmitAllowsSymlinkInsideRoot(t *testing.T) {
	req := validRequest()
	req.Config.Resources = []any{"safe-link"}
	if err := Admit(req); err != nil {
		t.Fatalf("expected in-root symlink to pass, got %v", err)
	}
}

func TestAdmitRejectsMutableRemoteRefs(t *testing.T) {
	tests := []struct {
		name   string
		ref    string
		reason string
	}{
		{"missing ref", "https://github.com/org/deploy", "ref"},
		{"branch ref", "https://github.com/org/deploy?ref=main", "40-character"},
		{"tag ref", "https://github.com/org/deploy?ref=v1.2.3", "40-character"},
		{"short sha", "https://github.com/org/deploy?ref=abc123", "40-character"},
		{"non hex sha", "https://github.com/org/deploy?ref=" + strings.Repeat("z", 40), "40-character"},
		{"http transport", "http://github.com/org/deploy?ref=" + pinnedCommit, "https"},
		{"unknown host", "https://evil.example.com/deploy?ref=" + pinnedCommit, "allowlist"},
		{"scp style", "git@github.com:org/deploy.git", "scp"},
		{"embedded user", "https://token@github.com/org/deploy?ref=" + pinnedCommit, "user"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Config.Resources = []any{tc.ref}
			err := Admit(req)
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !hasViolation(err, "resources", tc.reason) {
				t.Fatalf("expected violation mentioning %q, got %v", tc.reason, err)
			}
		})
	}
}

func TestAdmitRejectsExecPlugins(t *testing.T) {
	tests := []struct {
		name  string
		entry any
	}{
		{
			name: "exec field",
			entry: map[string]any{
				"exec": map[string]any{"command": "curl", "args": []any{"evil"}},
			},
		},
		{
			name: "exec annotation",
			entry: map[string]any{
				"apiVersion": "config.kubernetes.io/v1",
				"kind":       "Generator",
				"metadata": map[string]any{
					"annotations": map[string]any{
						"config.kubernetes.io/function": "exec: {command: sh}",
					},
				},
			},
		},
		{
			name: "exec kind",
			entry: map[string]any{
				"apiVersion": "builtin",
				"kind":       "Exec",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Config.Generators = []any{tc.entry}
			if err := Admit(req); err == nil || !hasViolation(err, "generators", "exec") {
				t.Fatalf("expected exec generator rejection, got %v", err)
			}

			req = validRequest()
			req.Config.Transformers = []any{tc.entry}
			if err := Admit(req); err == nil || !hasViolation(err, "transformers", "exec") {
				t.Fatalf("expected exec transformer rejection, got %v", err)
			}
		})
	}
}

func TestAdmitRejectsCycles(t *testing.T) {
	t.Run("self reference", func(t *testing.T) {
		req := validRequest()
		req.Config.Resources = []any{"."}
		err := Admit(req)
		if err == nil || !hasViolation(err, "references", "cyclic") {
			t.Fatalf("expected self cycle rejection, got %v", err)
		}
	})

	t.Run("multi file cycle", func(t *testing.T) {
		root := &Kustomization{Resources: []any{"base"}}
		base := &Kustomization{Resources: []any{"../overlay/dev"}}
		overlay := &Kustomization{Resources: []any{"../../base"}}
		req := &Request{
			Entry:        "kustomization.yaml",
			Config:       root,
			AllowedHosts: []string{"github.com"},
			Files:        baseFiles(),
			Configs: map[string]*Kustomization{
				"base/kustomization.yaml":        base,
				"overlay/dev/kustomization.yaml": overlay,
			},
		}
		err := Admit(req)
		if err == nil || !hasViolation(err, "references", "cyclic") {
			t.Fatalf("expected cross-file cycle rejection, got %v", err)
		}
	})

	t.Run("acyclic graph passes", func(t *testing.T) {
		root := &Kustomization{Resources: []any{"overlay/dev"}}
		overlay := &Kustomization{Resources: []any{"../../base"}}
		base := &Kustomization{}
		req := &Request{
			Entry:        "kustomization.yaml",
			Config:       root,
			AllowedHosts: []string{"github.com"},
			Files:        baseFiles(),
			Configs: map[string]*Kustomization{
				"base/kustomization.yaml":        base,
				"overlay/dev/kustomization.yaml": overlay,
			},
		}
		if err := Admit(req); err != nil {
			t.Fatalf("expected acyclic graph to pass, got %v", err)
		}
	})
}

func TestAdmitRejectsHelmChartProblems(t *testing.T) {
	tests := []struct {
		name   string
		chart  HelmChart
		reason string
	}{
		{
			name:   "missing repo",
			chart:  HelmChart{Name: "redis", Version: "1.0.0"},
			reason: "repo is required",
		},
		{
			name:   "missing version",
			chart:  HelmChart{Name: "redis", Repo: "https://charts.example.com"},
			reason: "version is required",
		},
		{
			name:   "unlisted host",
			chart:  HelmChart{Name: "redis", Version: "1.0.0", Repo: "https://evil.example.com"},
			reason: "allowlist",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			req.Config.HelmCharts = []HelmChart{tc.chart}
			err := Admit(req)
			if err == nil || !hasViolation(err, "helmCharts", tc.reason) {
				t.Fatalf("expected helm rejection for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestAdmitRejectsSymlinkLoop(t *testing.T) {
	files := []FileEntry{
		{Path: "kustomization.yaml"},
		{Path: "a", IsSymlink: true, LinkTarget: "b"},
		{Path: "b", IsSymlink: true, LinkTarget: "a"},
	}
	req := &Request{
		Entry:        "kustomization.yaml",
		Config:       &Kustomization{Resources: []any{"a"}},
		Files:        files,
		AllowedHosts: []string{"github.com"},
	}
	err := Admit(req)
	if err == nil || !hasViolation(err, "resources", "symlink loop") {
		t.Fatalf("expected symlink loop rejection, got %v", err)
	}
}

func hasViolation(err error, field, reasonPart string) bool {
	admissionErr, ok := err.(*AdmissionError)
	if !ok {
		return false
	}
	for _, v := range admissionErr.Violations {
		if strings.HasPrefix(v.Field, field) && strings.Contains(v.Error(), reasonPart) {
			return true
		}
	}
	return false
}
