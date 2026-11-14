package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	repoPrivileged = "acme/privileged"
	repoTrusted    = "acme/trusted"
	repoUntrusted  = "acme/untrusted"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	router, err := NewRouter(Config{
		PrivilegedLabels:       []string{"privileged", "prod-secrets"},
		PrivilegedCapabilities: []Capability{"secrets.read"},
		Repositories: []Repository{
			{
				ID:                repoPrivileged,
				Trust:             TrustPrivileged,
				ProtectedBranches: []string{"main", "release/*", "release/**", "feature/*/**"},
			},
			{
				ID:                repoTrusted,
				Trust:             TrustTrusted,
				ProtectedBranches: []string{"main"},
			},
			{
				ID:                repoUntrusted,
				Trust:             TrustUntrusted,
				ProtectedBranches: []string{"main"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	executors := []Executor{
		{
			ID:           "sandbox-a",
			Domain:       DomainUntrusted,
			Labels:       []string{"linux", "docker", "deploy"},
			Capabilities: []Capability{"build", "deploy"},
		},
		{
			ID:           "trusted-a",
			Domain:       DomainTrusted,
			Labels:       []string{"linux", "docker", "deploy"},
			Capabilities: []Capability{"build", "deploy"},
		},
		{
			ID:           "prod-a",
			Domain:       DomainProduction,
			Labels:       []string{"linux", "docker", "deploy", "privileged", "prod-secrets", "prod-only"},
			Capabilities: []Capability{"build", "deploy", "secrets.read", "prod-only-cap"},
		},
	}
	for _, executor := range executors {
		if err := router.RegisterExecutor(executor); err != nil {
			t.Fatalf("RegisterExecutor(%s): %v", executor.ID, err)
		}
	}
	return router
}

func jobFor(repository, branch string, labels []string, capabilities ...Capability) Job {
	return Job{
		ID:                   fmt.Sprintf("job-%s-%d", repository, len(branch)),
		Repository:           repository,
		Source:               SourceRepository,
		Branch:               branch,
		Labels:               labels,
		RequiredCapabilities: capabilities,
		Queue:                "build",
		CacheKey:             "deps",
		ArtifactKey:          "bundle",
	}
}

func assertExecutor(t *testing.T, router *Router, job Job, wantID string, wantDomain TrustDomain) Placement {
	t.Helper()
	placement, err := router.Route(job)
	if err != nil {
		t.Fatalf("Route(%+v): %v", job, err)
	}
	if placement.ExecutorID != wantID {
		t.Fatalf("Route(%+v) executor = %q, want %q", job, placement.ExecutorID, wantID)
	}
	if placement.Domain != wantDomain {
		t.Fatalf("Route(%+v) domain = %q, want %q", job, placement.Domain, wantDomain)
	}
	return placement
}

func TestForgedPrivilegedLabelsAreRejected(t *testing.T) {
	router := newTestRouter(t)

	tests := []struct {
		name    string
		job     Job
		wantErr error
	}{
		{
			name:    "untrusted repository forges privileged label",
			job:     jobFor(repoUntrusted, "feature", []string{"privileged"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "untrusted repository forges production secret label",
			job:     jobFor(repoUntrusted, "feature", []string{"prod-secrets"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "protected branch of untrusted repository still cannot use privileged label",
			job:     jobFor(repoUntrusted, "main", []string{"privileged"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "trusted repository cannot use privileged label",
			job:     jobFor(repoTrusted, "main", []string{"privileged"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "untrusted repository asks for privileged capability",
			job:     jobFor(repoUntrusted, "feature", []string{"linux"}, "secrets.read"),
			wantErr: ErrPrivilegedCapability,
		},
		{
			name: "fork targeting protected branch forges privileged label",
			job: Job{
				ID:         "fork-privileged",
				Repository: repoPrivileged,
				Source:     SourceFork,
				Ref:        "refs/heads/main",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrPrivilegedLabel,
		},
		{
			name: "fork targeting protected branch asks for secret capability",
			job: Job{
				ID:                   "fork-secrets",
				Repository:           repoPrivileged,
				Source:               SourceFork,
				Ref:                  "refs/heads/main",
				RequiredCapabilities: []Capability{"secrets.read"},
			},
			wantErr: ErrPrivilegedCapability,
		},
		{
			name: "unknown provenance cannot inherit repository trust",
			job: Job{
				ID:         "unknown-source",
				Repository: repoPrivileged,
				Source:     "",
				Ref:        "refs/heads/main",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrPrivilegedLabel,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			placement, err := router.Route(test.job)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Route() error = %v, want %v", err, test.wantErr)
			}
			if placement.ExecutorID != "" || placement.Domain != "" {
				t.Fatalf("denied job received a placement: %+v", placement)
			}
		})
	}
}

func TestOrdinaryLabelsCannotReachAnotherDomain(t *testing.T) {
	router := newTestRouter(t)
	job := jobFor(repoUntrusted, "feature", []string{"deploy"}, "deploy")
	assertExecutor(t, router, job, "sandbox-a", DomainUntrusted)
}

func TestProvenanceCapsEffectiveTrust(t *testing.T) {
	router := newTestRouter(t)

	fork := Job{
		ID:                   "fork-main",
		Repository:           repoPrivileged,
		Source:               SourceFork,
		Ref:                  "refs/heads/main",
		Labels:               []string{"deploy"},
		RequiredCapabilities: []Capability{"deploy"},
	}
	assertExecutor(t, router, fork, "sandbox-a", DomainUntrusted)

	unknownRepository := jobFor("acme/unregistered", "main", []string{"deploy"}, "deploy")
	assertExecutor(t, router, unknownRepository, "sandbox-a", DomainUntrusted)

	unknownSource := jobFor(repoPrivileged, "main", []string{"deploy"}, "deploy")
	unknownSource.Source = SourceUnknown
	assertExecutor(t, router, unknownSource, "sandbox-a", DomainUntrusted)
}

func TestBranchBypassAttemptsAreNotProtected(t *testing.T) {
	router := newTestRouter(t)

	tests := []struct {
		name    string
		job     Job
		wantErr error
	}{
		{
			name: "dot-dot traversal in ref",
			job: Job{
				ID:         "traversal-ref",
				Repository: repoPrivileged,
				Source:     SourceRepository,
				Ref:        "refs/heads/main/../main",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrInvalidRef,
		},
		{
			name:    "dot-dot traversal in branch",
			job:     jobFor(repoPrivileged, "main/../../release/1", []string{"privileged"}),
			wantErr: ErrInvalidRef,
		},
		{
			name:    "trailing slash",
			job:     jobFor(repoPrivileged, "main/", []string{"privileged"}),
			wantErr: ErrInvalidRef,
		},
		{
			name:    "trailing whitespace",
			job:     jobFor(repoPrivileged, "main ", []string{"privileged"}),
			wantErr: ErrInvalidRef,
		},
		{
			name:    "control character",
			job:     jobFor(repoPrivileged, "main\n", []string{"privileged"}),
			wantErr: ErrInvalidRef,
		},
		{
			name:    "case variant is a different branch",
			job:     jobFor(repoPrivileged, "MAIN", []string{"privileged"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "lookalike suffix",
			job:     jobFor(repoPrivileged, "main-fake", []string{"privileged"}),
			wantErr: ErrPrivilegedLabel,
		},
		{
			name:    "locked-looking name is rejected",
			job:     jobFor(repoPrivileged, "main.lock", []string{"privileged"}),
			wantErr: ErrInvalidRef,
		},
		{
			name: "tag is never a protected branch",
			job: Job{
				ID:         "tag",
				Repository: repoPrivileged,
				Source:     SourceRepository,
				Ref:        "refs/tags/main",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrPrivilegedLabel,
		},
		{
			name: "pull ref cannot borrow the base branch claim",
			job: Job{
				ID:         "pull-ref",
				Repository: repoPrivileged,
				Source:     SourceRepository,
				Ref:        "refs/pull/1/merge",
				Branch:     "main",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrPrivilegedLabel,
		},
		{
			name: "branch and ref disagreement",
			job: Job{
				ID:         "mismatch",
				Repository: repoPrivileged,
				Source:     SourceRepository,
				Ref:        "refs/heads/main",
				Branch:     "release/1.0",
				Labels:     []string{"privileged"},
			},
			wantErr: ErrInvalidRef,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			placement, err := router.Route(test.job)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Route() error = %v, want %v", err, test.wantErr)
			}
			if placement.ExecutorID != "" {
				t.Fatalf("bypass attempt was placed on %q", placement.ExecutorID)
			}
		})
	}
}

func TestCrossDomainBorrowingIsDenied(t *testing.T) {
	router := newTestRouter(t)

	assertExecutor(t,
		router,
		jobFor(repoUntrusted, "feature", []string{"linux"}, "build"),
		"sandbox-a",
		DomainUntrusted,
	)
	assertExecutor(t,
		router,
		jobFor(repoTrusted, "main", []string{"linux"}, "build"),
		"trusted-a",
		DomainTrusted,
	)
	assertExecutor(t,
		router,
		jobFor(repoPrivileged, "main", []string{"linux"}, "build"),
		"prod-a",
		DomainProduction,
	)

	// A label or capability that only exists in the production domain is not
	// enough to borrow a production executor for a trusted job.
	if _, err := router.Route(jobFor(repoTrusted, "main", []string{"prod-only"})); !errors.Is(err, ErrNoExecutor) {
		t.Fatalf("trusted job with prod-only label error = %v, want %v", err, ErrNoExecutor)
	}
	if _, err := router.Route(jobFor(repoTrusted, "main", []string{"linux"}, "prod-only-cap")); !errors.Is(err, ErrNoExecutor) {
		t.Fatalf("trusted job with prod-only capability error = %v, want %v", err, ErrNoExecutor)
	}

	// An executor ID cannot be moved between domains by updating it.
	err := router.UpdateExecutor(Executor{
		ID:     "prod-a",
		Domain: DomainTrusted,
		Labels: []string{"linux"},
	})
	if !errors.Is(err, ErrDomainChange) {
		t.Fatalf("UpdateExecutor domain change error = %v, want %v", err, ErrDomainChange)
	}

	// Nor can a second registration reuse the ID in another domain.
	err = router.RegisterExecutor(Executor{
		ID:     "prod-a",
		Domain: DomainTrusted,
		Labels: []string{"linux"},
	})
	if !errors.Is(err, ErrExecutorExists) {
		t.Fatalf("duplicate executor error = %v, want %v", err, ErrExecutorExists)
	}

	// A non-production executor must not advertise a privileged label.
	err = router.RegisterExecutor(Executor{
		ID:     "bad-trusted",
		Domain: DomainTrusted,
		Labels: []string{"privileged"},
	})
	if !errors.Is(err, ErrInvalidExecutor) {
		t.Fatalf("privileged executor label in trusted domain error = %v, want %v", err, ErrInvalidExecutor)
	}

	// Unknown domains are rejected rather than treated as an extra pool.
	err = router.RegisterExecutor(Executor{
		ID:     "unknown-domain",
		Domain: "production-copy",
	})
	if !errors.Is(err, ErrInvalidExecutor) || !errors.Is(err, ErrUnknownDomain) {
		t.Fatalf("unknown domain error = %v, want invalid executor/unknown domain", err)
	}

	// If no executor exists in the allowed domain, the router fails closed
	// instead of borrowing an executor from a lower domain.
	if err := router.RemoveExecutor("prod-a"); err != nil {
		t.Fatalf("RemoveExecutor: %v", err)
	}
	if _, err := router.Route(jobFor(repoPrivileged, "main", []string{"privileged"}, "secrets.read")); !errors.Is(err, ErrNoExecutor) {
		t.Fatalf("privileged job without production executor error = %v, want %v", err, ErrNoExecutor)
	}

	// A retired executor ID cannot reappear in another domain.
	err = router.RegisterExecutor(Executor{
		ID:     "prod-a",
		Domain: DomainTrusted,
		Labels: []string{"linux"},
	})
	if !errors.Is(err, ErrDomainChange) {
		t.Fatalf("retired executor domain change error = %v, want %v", err, ErrDomainChange)
	}
}

func TestLegitimateTrustedJobsReachOnlyAllowedExecutors(t *testing.T) {
	router := newTestRouter(t)

	placement := assertExecutor(t, router, jobFor(
		repoPrivileged,
		"main",
		[]string{"linux", "privileged"},
		"build",
		"secrets.read",
	), "prod-a", DomainProduction)

	if placement.Trust != TrustPrivileged {
		t.Fatalf("placement trust = %v, want privileged", placement.Trust)
	}
	if !placement.ProtectedBranch {
		t.Fatal("placement should be marked protected")
	}

	release := assertExecutor(t,
		router,
		jobFor(repoPrivileged, "release/1.2", []string{"privileged"}, "build"),
		"prod-a",
		DomainProduction,
	)
	if !release.ProtectedBranch {
		t.Fatal("release branch should be protected")
	}

	nested := assertExecutor(t,
		router,
		jobFor(repoPrivileged, "release/1.2/hotfix", []string{"privileged"}, "build"),
		"prod-a",
		DomainProduction,
	)
	if !nested.ProtectedBranch {
		t.Fatal("nested release branch should be protected by release/**")
	}

	wildcardBase := assertExecutor(t,
		router,
		jobFor(repoPrivileged, "feature/alpha/deep", []string{"privileged"}, "build"),
		"prod-a",
		DomainProduction,
	)
	if !wildcardBase.ProtectedBranch {
		t.Fatal("wildcard base branch should be protected by feature/*/**")
	}

	trusted := assertExecutor(t,
		router,
		jobFor(repoTrusted, "main", []string{"deploy"}, "deploy"),
		"trusted-a",
		DomainTrusted,
	)
	if trusted.Trust != TrustTrusted {
		t.Fatalf("trusted placement trust = %v", trusted.Trust)
	}

	assertExecutor(t,
		router,
		jobFor(repoUntrusted, "main", []string{"deploy"}, "deploy"),
		"sandbox-a",
		DomainUntrusted,
	)
}

func TestTrustChangesTakeEffectImmediately(t *testing.T) {
	router := newTestRouter(t)
	sensitive := jobFor(repoPrivileged, "main", []string{"privileged"}, "secrets.read")
	ordinary := jobFor(repoPrivileged, "main", []string{"deploy"}, "deploy")

	assertExecutor(t, router, sensitive, "prod-a", DomainProduction)

	if err := router.SetRepositoryTrust(repoPrivileged, TrustTrusted); err != nil {
		t.Fatalf("SetRepositoryTrust: %v", err)
	}
	if _, err := router.Route(sensitive); !errors.Is(err, ErrPrivilegedLabel) {
		t.Fatalf("after downgrade sensitive job error = %v, want %v", err, ErrPrivilegedLabel)
	}
	assertExecutor(t, router, ordinary, "trusted-a", DomainTrusted)

	decision, err := router.Resolve(ordinary)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if decision.Trust != TrustTrusted || decision.Domain != DomainTrusted {
		t.Fatalf("resolved decision after downgrade = %+v", decision)
	}

	if err := router.SetRepositoryTrust(repoPrivileged, TrustUntrusted); err != nil {
		t.Fatalf("SetRepositoryTrust: %v", err)
	}
	assertExecutor(t, router, ordinary, "sandbox-a", DomainUntrusted)
	if _, err := router.Route(sensitive); !errors.Is(err, ErrPrivilegedLabel) {
		t.Fatalf("after untrust sensitive job error = %v, want %v", err, ErrPrivilegedLabel)
	}

	if err := router.SetRepositoryTrust(repoPrivileged, TrustPrivileged); err != nil {
		t.Fatalf("SetRepositoryTrust: %v", err)
	}
	assertExecutor(t, router, sensitive, "prod-a", DomainProduction)

	// Removing branch protection also removes production access without
	// needing a trust change.
	if err := router.SetProtectedBranches(repoPrivileged, []string{"release/*"}); err != nil {
		t.Fatalf("SetProtectedBranches: %v", err)
	}
	if _, err := router.Route(sensitive); !errors.Is(err, ErrPrivilegedLabel) {
		t.Fatalf("after unprotecting main error = %v, want %v", err, ErrPrivilegedLabel)
	}
	assertExecutor(t, router, ordinary, "trusted-a", DomainTrusted)
	assertExecutor(t,
		router,
		jobFor(repoPrivileged, "release/2.0", []string{"privileged"}, "build"),
		"prod-a",
		DomainProduction,
	)
}

func TestQueueCacheAndArtifactScopesAreDomainIsolated(t *testing.T) {
	router := newTestRouter(t)

	sandbox := assertExecutor(t,
		router,
		jobFor(repoUntrusted, "feature", []string{"linux"}, "build"),
		"sandbox-a",
		DomainUntrusted,
	)
	trusted := assertExecutor(t,
		router,
		jobFor(repoTrusted, "main", []string{"linux"}, "build"),
		"trusted-a",
		DomainTrusted,
	)
	production := assertExecutor(t,
		router,
		jobFor(repoPrivileged, "main", []string{"linux"}, "build"),
		"prod-a",
		DomainProduction,
	)

	placements := []Placement{sandbox, trusted, production}
	for _, placement := range placements {
		if !strings.HasPrefix(placement.Queue, "queue/"+string(placement.Domain)+"/") {
			t.Fatalf("queue %q is not scoped to %q", placement.Queue, placement.Domain)
		}
		if !strings.HasPrefix(placement.CacheNamespace, "cache/"+string(placement.Domain)+"/") {
			t.Fatalf("cache %q is not scoped to %q", placement.CacheNamespace, placement.Domain)
		}
		if !strings.HasPrefix(placement.ArtifactNamespace, "artifacts/"+string(placement.Domain)+"/") {
			t.Fatalf("artifacts %q are not scoped to %q", placement.ArtifactNamespace, placement.Domain)
		}
	}

	seenQueues := map[string]struct{}{}
	seenCaches := map[string]struct{}{}
	seenArtifacts := map[string]struct{}{}
	for _, placement := range placements {
		for name, value := range map[string]string{
			"queue":     placement.Queue,
			"cache":     placement.CacheNamespace,
			"artifacts": placement.ArtifactNamespace,
		} {
			if strings.Contains(value, "..") {
				t.Fatalf("%s scope %q contains a traversal segment", name, value)
			}
		}
		seenQueues[placement.Queue] = struct{}{}
		seenCaches[placement.CacheNamespace] = struct{}{}
		seenArtifacts[placement.ArtifactNamespace] = struct{}{}
	}
	if len(seenQueues) != 3 || len(seenCaches) != 3 || len(seenArtifacts) != 3 {
		t.Fatalf("scope collision: queues=%v caches=%v artifacts=%v", seenQueues, seenCaches, seenArtifacts)
	}

	// Caller-controlled key components cannot escape their domain prefix.
	traversal := CacheNamespace(DomainTrusted, repoTrusted, "../../production/secret")
	if !strings.HasPrefix(traversal, "cache/"+string(DomainTrusted)+"/") {
		t.Fatalf("traversal key changed namespace prefix: %q", traversal)
	}
	if strings.Contains(traversal, "..") {
		t.Fatalf("traversal key survived escaping: %q", traversal)
	}
}

func TestConfigurationValidation(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "shared domain",
			config: Config{Domains: map[TrustLevel]TrustDomain{
				TrustUntrusted:  "shared",
				TrustTrusted:    "shared",
				TrustPrivileged: "production",
			}},
		},
		{
			name: "missing domain",
			config: Config{Domains: map[TrustLevel]TrustDomain{
				TrustUntrusted: TrustDomain("untrusted"),
			}},
		},
		{
			name: "invalid protection pattern",
			config: Config{Repositories: []Repository{{
				ID:                "acme/app",
				Trust:             TrustTrusted,
				ProtectedBranches: []string{"["},
			}}},
		},
		{
			name: "empty privileged label",
			config: Config{
				PrivilegedLabels: []string{""},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRouter(test.config); err == nil {
				t.Fatal("NewRouter accepted invalid config")
			}
		})
	}
}

func TestConcurrentRoutingAndTrustUpdates(t *testing.T) {
	router := newTestRouter(t)
	ordinary := jobFor(repoPrivileged, "main", []string{"deploy"}, "deploy")

	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(worker int) {
			for j := 0; j < 100; j++ {
				placement, err := router.Route(ordinary)
				if err != nil {
					errs <- fmt.Errorf("worker %d route: %w", worker, err)
					return
				}
				if placement.Domain != DomainProduction && placement.Domain != DomainTrusted {
					errs <- fmt.Errorf("worker %d routed to unexpected domain %q", worker, placement.Domain)
					return
				}
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < 20; i++ {
		trust := TrustPrivileged
		if i%2 == 0 {
			trust = TrustTrusted
		}
		if err := router.SetRepositoryTrust(repoPrivileged, trust); err != nil {
			t.Fatalf("SetRepositoryTrust: %v", err)
		}
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
