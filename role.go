package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Claims is the subset of an already-verified GitHub Actions OIDC token that
// is relevant to role selection.
type Claims struct {
	Sub               string `json:"sub"`
	Repository        string `json:"repository"`
	RepositoryOwner   string `json:"repository_owner,omitempty"`
	Environment       string `json:"environment,omitempty"`
	EventName         string `json:"event_name,omitempty"`
	RunnerEnvironment string `json:"runner_environment,omitempty"`
	Ref               string `json:"ref,omitempty"`
}

// Role describes the conditions under which a release role may be issued.
//
// An environment is matched literally against AllowedEnvironments. A value of
// "*" in the allow-list is not a wildcard and can never match a valid
// environment because wildcard characters are rejected in environment names.
//
// HostedOnly marks a role that may only be issued to GitHub-hosted runners.
type Role struct {
	ID                  string   `json:"id,omitempty"`
	Name                string   `json:"name,omitempty"`
	AllowedEnvironments []string `json:"allowed_environments"`
	HostedOnly          bool     `json:"hosted_only"`

	// Environments and HostedRunnerOnly are accepted aliases for
	// AllowedEnvironments and HostedOnly. They make configuration decoding
	// tolerant of older role names.
	Environments     []string `json:"environments,omitempty"`
	HostedRunnerOnly bool     `json:"hosted_runner_only,omitempty"`
}

// RolePolicy is an alias retained for callers that describe their role
// configuration as a policy.
type RolePolicy = Role

// Policy is an alias for RolePolicy.
type Policy = Role

// Reason is a stable rejection reason suitable for logs and tests.
type Reason string

const (
	ReasonAllowed               Reason = "allowed"
	ReasonInvalidClaims         Reason = "invalid_claims"
	ReasonInvalidRole           Reason = "invalid_role"
	ReasonMissingSub            Reason = "missing_sub"
	ReasonMissingRepository     Reason = "missing_repository"
	ReasonInvalidRepository     Reason = "invalid_repository"
	ReasonMissingEnvironment    Reason = "missing_environment"
	ReasonInvalidEnvironment    Reason = "invalid_environment"
	ReasonNonEnvironmentSubject Reason = "non_environment_subject"
	ReasonSubjectMismatch       Reason = "subject_mismatch"
	ReasonPullRequestTarget     Reason = "pull_request_target"
	ReasonEnvironmentNotAllowed Reason = "environment_not_allowed"
	ReasonSelfHostedRunner      Reason = "self_hosted_runner"
	ReasonRunnerNotHosted       Reason = "runner_not_github_hosted"
)

// RoleError reports why a claim set did not satisfy a role.
type RoleError struct {
	Reason Reason
	Detail string
}

func (e *RoleError) Error() string {
	msg := "netsec: role condition rejected: " + string(e.Reason)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// Is lets errors.Is compare the stable rejection reason rather than the
// human-readable detail.
func (e *RoleError) Is(target error) bool {
	t, ok := target.(*RoleError)
	return ok && e.Reason == t.Reason
}

// Sentinel errors may be used with errors.Is.
var (
	ErrInvalidClaims         = &RoleError{Reason: ReasonInvalidClaims}
	ErrInvalidRole           = &RoleError{Reason: ReasonInvalidRole}
	ErrMissingSub            = &RoleError{Reason: ReasonMissingSub}
	ErrMissingRepository     = &RoleError{Reason: ReasonMissingRepository}
	ErrInvalidRepository     = &RoleError{Reason: ReasonInvalidRepository}
	ErrMissingEnvironment    = &RoleError{Reason: ReasonMissingEnvironment}
	ErrInvalidEnvironment    = &RoleError{Reason: ReasonInvalidEnvironment}
	ErrNonEnvironmentSubject = &RoleError{Reason: ReasonNonEnvironmentSubject}
	ErrSubjectMismatch       = &RoleError{Reason: ReasonSubjectMismatch}
	ErrPullRequestTarget     = &RoleError{Reason: ReasonPullRequestTarget}
	ErrEnvironmentNotAllowed = &RoleError{Reason: ReasonEnvironmentNotAllowed}
	ErrSelfHostedRunner      = &RoleError{Reason: ReasonSelfHostedRunner}
	ErrRunnerNotHosted       = &RoleError{Reason: ReasonRunnerNotHosted}
)

// Decision is the result of evaluating a claim set against a role.
type Decision struct {
	Allowed bool
	Reason  Reason
	Detail  string
}

// Error returns nil for an allowed decision and a *RoleError otherwise.
func (d Decision) Error() error {
	if d.Allowed {
		return nil
	}
	return &RoleError{Reason: d.Reason, Detail: d.Detail}
}

const (
	subjectRepositoryPrefix  = "repo:"
	subjectEnvironmentMarker = ":environment:"
)

// EnvironmentSubject builds the only subject shape accepted as an
// environment identity: repo:<repository>:environment:<environment>.
func EnvironmentSubject(repository, environment string) string {
	return subjectRepositoryPrefix + repository + subjectEnvironmentMarker + environment
}

// BuildEnvironmentSubject is an alias for EnvironmentSubject.
func BuildEnvironmentSubject(repository, environment string) string {
	return EnvironmentSubject(repository, environment)
}

// Evaluate checks verified claims against a role. Claims may be a Claims
// value, *Claims, map[string]string, map[string]any, JSON bytes, or a struct
// with the GitHub OIDC JSON field names. Role may be a Role, *Role, []string
// of allowed environments, or JSON-decodable role configuration. When
// provided, the optional hostedOnly value overrides the role's hosted-only
// flag.
func Evaluate(claims, role any, hostedOnly ...bool) Decision {
	c, err := normalizeClaims(claims)
	if err != nil {
		return deny(ReasonInvalidClaims, err.Error())
	}

	r, err := normalizeRole(role)
	if err != nil {
		return deny(ReasonInvalidRole, err.Error())
	}
	if len(hostedOnly) > 0 {
		r.HostedOnly = hostedOnly[len(hostedOnly)-1]
		r.HostedRunnerOnly = false
	}

	return evaluateClaims(c, r)
}

// Authorize returns nil only when the claims satisfy every role condition.
func Authorize(claims, role any, hostedOnly ...bool) error {
	return Evaluate(claims, role, hostedOnly...).Error()
}

// AuthorizeRole is an explicit name for Authorize.
func AuthorizeRole(claims, role any, hostedOnly ...bool) error {
	return Authorize(claims, role, hostedOnly...)
}

// EvaluateRole is an error-returning alias for Authorize.
func EvaluateRole(claims, role any, hostedOnly ...bool) error {
	return Authorize(claims, role, hostedOnly...)
}

// ValidateRole is an error-returning alias for Authorize.
func ValidateRole(claims, role any, hostedOnly ...bool) error {
	return Authorize(claims, role, hostedOnly...)
}

// Validate is an error-returning alias for Authorize.
func Validate(claims, role any, hostedOnly ...bool) error {
	return Authorize(claims, role, hostedOnly...)
}

// CheckRole reports whether the claims satisfy the role conditions.
func CheckRole(claims, role any, hostedOnly ...bool) bool {
	return Evaluate(claims, role, hostedOnly...).Allowed
}

// RoleAllowed reports whether the claims satisfy the role conditions.
func RoleAllowed(claims, role any, hostedOnly ...bool) bool {
	return CheckRole(claims, role, hostedOnly...)
}

// CanAssumeRole reports whether the claims satisfy the role conditions.
func CanAssumeRole(claims, role any, hostedOnly ...bool) bool {
	return CheckRole(claims, role, hostedOnly...)
}

// IsAllowed reports whether the claims satisfy the role conditions.
func IsAllowed(claims, role any, hostedOnly ...bool) bool {
	return CheckRole(claims, role, hostedOnly...)
}

func evaluateClaims(c Claims, r Role) Decision {
	if c.Sub == "" {
		return deny(ReasonMissingSub, "the sub claim is required")
	}
	if c.Repository == "" {
		return deny(ReasonMissingRepository, "the repository claim is required")
	}
	if !validRepository(c.Repository) {
		return deny(ReasonInvalidRepository, "repository must be a single owner/name value without ':' or '*'")
	}
	if c.Environment == "" {
		return deny(ReasonMissingEnvironment, "an environment identity requires a declared environment")
	}
	if !validEnvironmentName(c.Environment) {
		return deny(ReasonInvalidEnvironment, "environment must not contain ':' or '*'")
	}
	if strings.EqualFold(c.EventName, "pull_request_target") {
		return deny(ReasonPullRequestTarget, "pull_request_target workflows cannot use environment identity")
	}
	if isRefOrPullRequestSubject(c.Sub) {
		return deny(ReasonNonEnvironmentSubject, "sub is a ref or pull_request subject, not an environment identity")
	}

	expected := EnvironmentSubject(c.Repository, c.Environment)
	if c.Sub != expected {
		return deny(ReasonSubjectMismatch, fmt.Sprintf("sub %q does not exactly equal %q", c.Sub, expected))
	}

	if !r.allowsEnvironment(c.Environment) {
		return deny(ReasonEnvironmentNotAllowed, fmt.Sprintf("environment %q is not allowed for this role", c.Environment))
	}

	if r.requiresHostedRunner() {
		switch {
		case strings.EqualFold(c.RunnerEnvironment, "self-hosted"):
			return deny(ReasonSelfHostedRunner, "self-hosted runners cannot assume a hosted-only role")
		case !strings.EqualFold(c.RunnerEnvironment, "github-hosted"):
			return deny(ReasonRunnerNotHosted, "hosted-only roles require runner_environment=github-hosted")
		}
	}

	return Decision{Allowed: true, Reason: ReasonAllowed}
}

func deny(reason Reason, detail string) Decision {
	return Decision{Reason: reason, Detail: detail}
}

func validRepository(repository string) bool {
	if strings.ContainsAny(repository, ":*") {
		return false
	}
	owner, name, ok := strings.Cut(repository, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

func validEnvironmentName(environment string) bool {
	return !strings.ContainsAny(environment, ":*")
}

func isRefOrPullRequestSubject(sub string) bool {
	if strings.HasPrefix(sub, "ref:") ||
		strings.HasPrefix(sub, "refs/") ||
		strings.HasPrefix(sub, "pull_request") {
		return true
	}
	if strings.Contains(sub, ":ref:") {
		return true
	}
	if strings.HasSuffix(sub, ":pull_request") || strings.Contains(sub, ":pull_request:") {
		return true
	}
	return false
}

func (r Role) allowsEnvironment(environment string) bool {
	for _, allowed := range r.allowedEnvironmentList() {
		if allowed == environment {
			return true
		}
	}
	return false
}

func (r Role) allowedEnvironmentList() []string {
	list := make([]string, 0, len(r.AllowedEnvironments)+len(r.Environments))
	list = append(list, r.AllowedEnvironments...)
	list = append(list, r.Environments...)
	return list
}

func (r Role) requiresHostedRunner() bool {
	return r.HostedOnly || r.HostedRunnerOnly
}

func normalizeClaims(v any) (Claims, error) {
	switch c := v.(type) {
	case nil:
		return Claims{}, errors.New("claims are nil")
	case Claims:
		return c, nil
	case *Claims:
		if c == nil {
			return Claims{}, errors.New("claims are nil")
		}
		return *c, nil
	case map[string]string:
		return claimsFromStrings(c), nil
	case map[string]any:
		return claimsFromAnyMap(c), nil
	case json.RawMessage:
		return claimsFromJSON(c)
	case []byte:
		return claimsFromJSON(c)
	case string:
		return claimsFromJSON([]byte(c))
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return Claims{}, fmt.Errorf("encode claims: %w", err)
		}
		return claimsFromJSON(raw)
	}
}

func claimsFromJSON(raw []byte) (Claims, error) {
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, fmt.Errorf("decode claims: %w", err)
	}
	return c, nil
}

func claimsFromStrings(m map[string]string) Claims {
	return Claims{
		Sub:               firstValue(m["sub"]),
		Repository:        firstValue(m["repository"]),
		RepositoryOwner:   firstValue(m["repository_owner"]),
		Environment:       firstValue(m["environment"]),
		EventName:         firstValue(m["event_name"], m["eventName"]),
		RunnerEnvironment: firstValue(m["runner_environment"], m["runnerEnvironment"]),
		Ref:               firstValue(m["ref"]),
	}
}

func claimsFromAnyMap(m map[string]any) Claims {
	return Claims{
		Sub:               stringClaim(m, "sub"),
		Repository:        stringClaim(m, "repository"),
		RepositoryOwner:   stringClaim(m, "repository_owner", "repositoryOwner"),
		Environment:       stringClaim(m, "environment"),
		EventName:         stringClaim(m, "event_name", "eventName"),
		RunnerEnvironment: stringClaim(m, "runner_environment", "runnerEnvironment"),
		Ref:               stringClaim(m, "ref"),
	}
}

func stringClaim(m map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch value := v.(type) {
		case string:
			return value
		case json.Number:
			return value.String()
		case []byte:
			return string(value)
		}
	}
	return ""
}

func firstValue(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeRole(v any) (Role, error) {
	switch r := v.(type) {
	case nil:
		return Role{}, errors.New("role is nil")
	case Role:
		return r, nil
	case *Role:
		if r == nil {
			return Role{}, errors.New("role is nil")
		}
		return *r, nil
	case []string:
		return Role{AllowedEnvironments: append([]string(nil), r...)}, nil
	case []any:
		allowed := make([]string, 0, len(r))
		for _, entry := range r {
			value, ok := entry.(string)
			if !ok {
				return Role{}, fmt.Errorf("allowed environment %v is not a string", entry)
			}
			allowed = append(allowed, value)
		}
		return Role{AllowedEnvironments: allowed}, nil
	case string:
		return Role{AllowedEnvironments: []string{r}}, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return Role{}, fmt.Errorf("encode role: %w", err)
		}
		var role Role
		if err := json.Unmarshal(raw, &role); err != nil {
			return Role{}, fmt.Errorf("decode role: %w", err)
		}
		return role, nil
	}
}
