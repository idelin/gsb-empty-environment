package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0275"
)

const (
	repository  = "acme/widgets"
	production  = "production"
	pullRequest = "repo:acme/widgets:pull_request"
	branchRef   = "repo:acme/widgets:ref:refs/heads/main"
)

func releaseRole() netsec.Role {
	return netsec.Role{
		ID:                  "release",
		AllowedEnvironments: []string{production},
		HostedOnly:          true,
	}
}

func productionClaims() netsec.Claims {
	return netsec.Claims{
		Sub:               netsec.EnvironmentSubject(repository, production),
		Repository:        repository,
		RepositoryOwner:   "acme",
		Environment:       production,
		EventName:         "push",
		RunnerEnvironment: "github-hosted",
		Ref:               "refs/heads/main",
	}
}

func TestAuthorizeAllowsExactEnvironmentIdentity(t *testing.T) {
	if err := netsec.Authorize(productionClaims(), releaseRole()); err != nil {
		t.Fatalf("expected exact environment identity to be allowed, got %v", err)
	}
	if !netsec.CheckRole(productionClaims(), releaseRole()) {
		t.Fatal("CheckRole rejected an exact environment identity")
	}
}

func TestAuthorizeRejectsEnvironmentInjection(t *testing.T) {
	tests := []struct {
		name        string
		environment string
	}{
		{name: "colon", environment: "production:staging"},
		{name: "colon prefix", environment: ":production"},
		{name: "star", environment: "production*"},
		{name: "star only", environment: "*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := productionClaims()
			claims.Environment = tt.environment
			claims.Sub = netsec.EnvironmentSubject(repository, tt.environment)

			role := releaseRole()
			role.AllowedEnvironments = append(role.AllowedEnvironments, tt.environment)

			err := netsec.Authorize(claims, role)
			if !errors.Is(err, netsec.ErrInvalidEnvironment) {
				t.Fatalf("expected ErrInvalidEnvironment, got %v", err)
			}
		})
	}
}

func TestAuthorizeRejectsColonInRepository(t *testing.T) {
	claims := productionClaims()
	claims.Repository = "acme/widgets:environment:production"
	claims.Sub = netsec.EnvironmentSubject(claims.Repository, claims.Environment)

	err := netsec.Authorize(claims, releaseRole())
	if !errors.Is(err, netsec.ErrInvalidRepository) {
		t.Fatalf("expected ErrInvalidRepository, got %v", err)
	}
}

func TestAuthorizeRejectsNonEnvironmentSubjects(t *testing.T) {
	tests := []struct {
		name string
		sub  string
	}{
		{name: "repo ref", sub: branchRef},
		{name: "bare ref", sub: "ref:refs/heads/main"},
		{name: "refs prefix", sub: "refs/heads/main"},
		{name: "pull request", sub: pullRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := productionClaims()
			claims.Sub = tt.sub

			err := netsec.Authorize(claims, releaseRole())
			if !errors.Is(err, netsec.ErrNonEnvironmentSubject) {
				t.Fatalf("expected ErrNonEnvironmentSubject, got %v", err)
			}
		})
	}
}

func TestAuthorizeRejectsBranchTriggerIdentityMismatch(t *testing.T) {
	claims := productionClaims()
	claims.EventName = "push"
	claims.Sub = branchRef
	claims.Environment = ""

	// A branch-triggered job cannot claim a protected environment identity.
	err := netsec.Authorize(claims, releaseRole())
	if err == nil {
		t.Fatal("expected a branch-triggered job without environment to be rejected")
	}
}

func TestAuthorizeRejectsMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*netsec.Claims)
		want   error
	}{
		{
			name:   "missing sub",
			mutate: func(c *netsec.Claims) { c.Sub = "" },
			want:   netsec.ErrMissingSub,
		},
		{
			name:   "missing repository",
			mutate: func(c *netsec.Claims) { c.Repository = "" },
			want:   netsec.ErrMissingRepository,
		},
		{
			name:   "missing environment",
			mutate: func(c *netsec.Claims) { c.Environment = "" },
			want:   netsec.ErrMissingEnvironment,
		},
		{
			name: "environment omitted from subject",
			mutate: func(c *netsec.Claims) {
				c.Environment = ""
				c.Sub = "repo:acme/widgets:environment:"
			},
			want: netsec.ErrMissingEnvironment,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := productionClaims()
			tt.mutate(&claims)

			err := netsec.Authorize(claims, releaseRole())
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func TestAuthorizeRejectsPullRequestTarget(t *testing.T) {
	claims := productionClaims()
	claims.EventName = "pull_request_target"

	err := netsec.Authorize(claims, releaseRole())
	if !errors.Is(err, netsec.ErrPullRequestTarget) {
		t.Fatalf("expected ErrPullRequestTarget, got %v", err)
	}
}

func TestAuthorizeRequiresExactSubject(t *testing.T) {
	tests := []struct {
		name string
		sub  string
	}{
		{
			name: "different environment in sub",
			sub:  netsec.EnvironmentSubject(repository, "staging"),
		},
		{
			name: "trailing text",
			sub:  netsec.EnvironmentSubject(repository, production) + ":staging",
		},
		{
			name: "different repository in sub",
			sub:  netsec.EnvironmentSubject("acme/other", production),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := productionClaims()
			claims.Sub = tt.sub

			err := netsec.Authorize(claims, releaseRole())
			if !errors.Is(err, netsec.ErrSubjectMismatch) {
				t.Fatalf("expected ErrSubjectMismatch, got %v", err)
			}
		})
	}
}

func TestAuthorizeRejectsEnvironmentOutsideRoleSet(t *testing.T) {
	claims := productionClaims()
	claims.Environment = "staging"
	claims.Sub = netsec.EnvironmentSubject(repository, claims.Environment)

	err := netsec.Authorize(claims, releaseRole())
	if !errors.Is(err, netsec.ErrEnvironmentNotAllowed) {
		t.Fatalf("expected ErrEnvironmentNotAllowed, got %v", err)
	}
}

func TestAuthorizeDoesNotTreatWildcardAsMatch(t *testing.T) {
	role := releaseRole()
	role.AllowedEnvironments = []string{"*"}

	err := netsec.Authorize(productionClaims(), role)
	if !errors.Is(err, netsec.ErrEnvironmentNotAllowed) {
		t.Fatalf("expected wildcard allow-list entry not to match, got %v", err)
	}
}

func TestAuthorizeRejectsSelfHostedRunnerForHostedOnlyRole(t *testing.T) {
	claims := productionClaims()
	claims.RunnerEnvironment = "self-hosted"

	err := netsec.Authorize(claims, releaseRole())
	if !errors.Is(err, netsec.ErrSelfHostedRunner) {
		t.Fatalf("expected ErrSelfHostedRunner, got %v", err)
	}
}

func TestAuthorizeRequiresGithubHostedForHostedOnlyRole(t *testing.T) {
	claims := productionClaims()
	claims.RunnerEnvironment = ""

	err := netsec.Authorize(claims, releaseRole())
	if !errors.Is(err, netsec.ErrRunnerNotHosted) {
		t.Fatalf("expected ErrRunnerNotHosted for missing runner_environment, got %v", err)
	}
}

func TestAuthorizeAllowsSelfHostedRunnerForRegularRole(t *testing.T) {
	claims := productionClaims()
	claims.RunnerEnvironment = "self-hosted"
	role := releaseRole()
	role.HostedOnly = false

	if err := netsec.Authorize(claims, role); err != nil {
		t.Fatalf("self-hosted runner should be allowed for a non-hosted-only role: %v", err)
	}
}

func TestAuthorizeSupportsRoleAliases(t *testing.T) {
	claims := productionClaims()
	claims.RunnerEnvironment = "self-hosted"

	role := netsec.Role{
		Environments:     []string{production},
		HostedRunnerOnly: true,
	}
	err := netsec.Authorize(claims, role)
	if !errors.Is(err, netsec.ErrSelfHostedRunner) {
		t.Fatalf("expected aliased hosted-only flag to reject self-hosted runner, got %v", err)
	}
}

func TestAuthorizeSupportsMapsAndJSON(t *testing.T) {
	claims := productionClaims()
	role := releaseRole()

	asStrings := map[string]string{
		"sub":                claims.Sub,
		"repository":         claims.Repository,
		"environment":        claims.Environment,
		"event_name":         claims.EventName,
		"runner_environment": claims.RunnerEnvironment,
	}
	asAny := map[string]any{
		"sub":                claims.Sub,
		"repository":         claims.Repository,
		"environment":        claims.Environment,
		"event_name":         claims.EventName,
		"runner_environment": claims.RunnerEnvironment,
	}
	asJSON := []byte(`{
		"sub": "repo:acme/widgets:environment:production",
		"repository": "acme/widgets",
		"environment": "production",
		"event_name": "push",
		"runner_environment": "github-hosted"
	}`)

	for name, input := range map[string]any{
		"map[string]string": asStrings,
		"map[string]any":    asAny,
		"json":              asJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if err := netsec.Authorize(input, role); err != nil {
				t.Fatalf("expected %T claims to be allowed: %v", input, err)
			}
		})
	}
}

func TestAuthorizeAcceptsPointerAndEnvironmentList(t *testing.T) {
	claims := productionClaims()
	role := releaseRole()

	if err := netsec.AuthorizeRole(&claims, &role); err != nil {
		t.Fatalf("expected pointer inputs to be allowed: %v", err)
	}
	if err := netsec.ValidateRole(claims, []string{production}); err != nil {
		t.Fatalf("expected an environment list to be accepted: %v", err)
	}
}

func TestAuthorizeAcceptsExplicitHostedOnlyFlag(t *testing.T) {
	claims := productionClaims()
	claims.RunnerEnvironment = "self-hosted"

	err := netsec.Authorize(claims, []string{production}, true)
	if !errors.Is(err, netsec.ErrSelfHostedRunner) {
		t.Fatalf("expected explicit hosted-only flag to reject self-hosted runner, got %v", err)
	}
}
