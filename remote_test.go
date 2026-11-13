package netsec

import "testing"

func TestRemoteReferencePolicy(t *testing.T) {
	hosts := []string{"github.com", "raw.githubusercontent.com"}
	admitter := NewAdmitter(testCatalog(t, nil), hosts)
	allowedURL := "https://github.com/example/deploy.git?ref=" + pinnedCommit

	t.Run("pinned https commit on allowlisted host is allowed", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{allowedURL},
			Bases:     []string{"https://raw.githubusercontent.com/example/deploy?ref=" + pinnedCommit},
		})
		assertAllowed(t, err)
	})

	t.Run("uppercase commit hash accepted", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy?ref=ABCDEF0123456789ABCDEF0123456789ABCDEF01"},
		})
		assertAllowed(t, err)
	})

	t.Run("branch ref rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy.git?ref=main"},
		})
		assertViolation(t, err, ReasonRefNotCommit)
	})

	t.Run("tag ref rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy.git?ref=v1.2.3"},
		})
		assertViolation(t, err, ReasonRefNotCommit)
	})

	t.Run("missing ref rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy.git"},
		})
		assertViolation(t, err, ReasonRefMissing)
	})

	t.Run("short hash rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy.git?ref=0123abcd"},
		})
		assertViolation(t, err, ReasonRefNotCommit)
	})

	t.Run("41 hex characters rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://github.com/example/deploy.git?ref=" + pinnedCommit + "f"},
		})
		assertViolation(t, err, ReasonRefNotCommit)
	})

	t.Run("http scheme rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"http://github.com/example/deploy.git?ref=" + pinnedCommit},
		})
		assertViolation(t, err, ReasonSchemeNotHTTPS)
	})

	t.Run("host outside allowlist rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://evil.example.com/deploy.git?ref=" + pinnedCommit},
		})
		assertViolation(t, err, ReasonHostNotAllowed)
	})

	t.Run("empty allowlist denies remote", func(t *testing.T) {
		strict := NewAdmitter(testCatalog(t, nil), nil)
		err := strict.Validate(&Kustomization{Resources: []string{allowedURL}})
		assertViolation(t, err, ReasonHostNotAllowed)
	})

	t.Run("scp style remote rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"git@github.com:example/deploy.git"},
		})
		assertViolation(t, err, ReasonSchemeNotHTTPS)
	})

	t.Run("kustomize host form rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"github.com/example/deploy?ref=" + pinnedCommit},
		})
		assertViolation(t, err, ReasonSchemeNotHTTPS)
	})

	t.Run("userinfo rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Resources: []string{"https://token:x@github.com/example/deploy?ref=" + pinnedCommit},
		})
		assertViolation(t, err, ReasonURLHasUserInfo)
	})

	t.Run("remote patch rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Patches: []Patch{{Path: allowedURL}},
		})
		assertViolation(t, err, ReasonRemoteNotAllowed)
	})

	t.Run("inline patch without path is allowed", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			Patches: []Patch{{Patch: "- op: replace\n  path: /replicas\n  value: 3"}},
		})
		assertAllowed(t, err)
	})
}

func TestHelmChartPolicy(t *testing.T) {
	admitter := NewAdmitter(testCatalog(t, nil), []string{"charts.example.com"})

	t.Run("allowlisted repo with version allowed", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			HelmCharts: []HelmChart{{
				Name:    "redis",
				Repo:    "https://charts.example.com/stable",
				Version: "18.19.0",
			}},
		})
		assertAllowed(t, err)
	})

	t.Run("missing version rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			HelmCharts: []HelmChart{{
				Name: "redis",
				Repo: "https://charts.example.com/stable",
			}},
		})
		assertViolation(t, err, ReasonHelmVersion)
	})

	t.Run("repo outside allowlist rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			HelmCharts: []HelmChart{{
				Name:    "redis",
				Repo:    "https://evil.example.com/charts",
				Version: "1.0.0",
			}},
		})
		assertViolation(t, err, ReasonHelmRepo)
	})

	t.Run("http repo rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			HelmCharts: []HelmChart{{
				Name:    "redis",
				Repo:    "http://charts.example.com/stable",
				Version: "1.0.0",
			}},
		})
		assertViolation(t, err, ReasonHelmRepo)
	})

	t.Run("missing repo rejected", func(t *testing.T) {
		err := admitter.Validate(&Kustomization{
			HelmCharts: []HelmChart{{Name: "redis", Version: "1.0.0"}},
		})
		assertViolation(t, err, ReasonHelmRepo)
	})
}
