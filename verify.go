package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	ErrInvalidPolicy                 = errors.New("invalid admission policy")
	ErrInvalidStatement              = errors.New("invalid SLSA provenance statement")
	ErrUntrustedBuilder              = errors.New("builder.id is not in the trusted builder table")
	ErrBuildTypeMismatch             = errors.New("buildType does not match the trusted builder entry")
	ErrInternalParametersPresent     = errors.New("internalParameters must not be present")
	ErrUnregisteredExternalParameter = errors.New("externalParameters contains an unregistered key")
	ErrSourceCommitMissing           = errors.New("resolved dependency digest does not contain the declared source commit")
)

// Verify applies policy to a decoded SLSA v1 statement.
//
// It returns a wrapped sentinel error for each rejection reason, so callers
// can use errors.Is to map the result to an admission response.
func Verify(statement *Statement, policy Policy) error {
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if statement == nil {
		return fmt.Errorf("%w: nil statement", ErrInvalidStatement)
	}
	if err := verifyStatementTypes(statement, policy); err != nil {
		return err
	}

	build := statement.Predicate.BuildDefinition
	builderID := statement.Predicate.RunDetails.Builder.ID
	if err := verifyBuilder(builderID, build.BuildType, policy); err != nil {
		return err
	}
	if len(build.InternalParameters) != 0 {
		return fmt.Errorf("%w: buildDefinition.internalParameters", ErrInternalParametersPresent)
	}
	if err := verifyExternalParameters(build.ExternalParameters, policy); err != nil {
		return err
	}
	if err := verifySourceDependency(build.ResolvedDependencies, policy); err != nil {
		return err
	}
	return nil
}

// VerifyProvenance is an alias for Verify, using the wording common in
// admission services.
func VerifyProvenance(statement *Statement, policy Policy) error {
	return Verify(statement, policy)
}

// Compare is an alias for Verify. It compares the claimed provenance against
// the trusted builder table and source requirements.
func Compare(statement *Statement, policy Policy) error {
	return Verify(statement, policy)
}

// VerifyJSON parses and verifies a JSON-encoded SLSA v1 statement.
func VerifyJSON(data []byte, policy Policy) error {
	statement, err := ParseStatement(data)
	if err != nil {
		return err
	}
	return Verify(statement, policy)
}

// VerifyBytes is an alias for VerifyJSON.
func VerifyBytes(data []byte, policy Policy) error {
	return VerifyJSON(data, policy)
}

// Matches reports whether a statement passes policy.
func Matches(statement *Statement, policy Policy) bool {
	return Verify(statement, policy) == nil
}

func validatePolicy(policy Policy) error {
	if len(policy.TrustedBuilders) == 0 {
		return fmt.Errorf("%w: trusted builder table is empty", ErrInvalidPolicy)
	}
	for id, buildType := range policy.TrustedBuilders {
		if id == "" {
			return fmt.Errorf("%w: trusted builder ID is empty", ErrInvalidPolicy)
		}
		if buildType == "" {
			return fmt.Errorf("%w: trusted buildType for %q is empty", ErrInvalidPolicy, id)
		}
	}
	if policy.SourceCommit == "" {
		return fmt.Errorf("%w: source commit is required", ErrInvalidPolicy)
	}
	return nil
}

func verifyStatementTypes(statement *Statement, policy Policy) error {
	if statement.Type == "" {
		if policy.RequireStatementTypes {
			return fmt.Errorf("%w: _type is required", ErrInvalidStatement)
		}
	} else if statement.Type != StatementType {
		return fmt.Errorf("%w: unexpected _type %q", ErrInvalidStatement, statement.Type)
	}

	if statement.PredicateType == "" {
		if policy.RequireStatementTypes {
			return fmt.Errorf("%w: predicateType is required", ErrInvalidStatement)
		}
	} else if statement.PredicateType != PredicateType {
		return fmt.Errorf("%w: unexpected predicateType %q", ErrInvalidStatement, statement.PredicateType)
	}
	return nil
}

func verifyBuilder(builderID, buildType string, policy Policy) error {
	expectedType, trusted := policy.TrustedBuilders[builderID]
	if !trusted {
		return fmt.Errorf("%w: %q", ErrUntrustedBuilder, builderID)
	}
	if buildType != expectedType {
		return fmt.Errorf("%w: builder %q requires %q, got %q", ErrBuildTypeMismatch, builderID, expectedType, buildType)
	}
	return nil
}

func decodeParameterObject(raw json.RawMessage, field string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, fmt.Errorf("%w: %s must be a JSON object: %v", ErrInvalidStatement, field, err)
	}
	return object, nil
}

func verifyExternalParameters(raw json.RawMessage, policy Policy) error {
	parameters, err := decodeParameterObject(raw, "buildDefinition.externalParameters")
	if err != nil {
		return err
	}

	allowed := make(map[string]struct{}, len(policy.AllowedExternalParameters))
	for _, key := range policy.AllowedExternalParameters {
		allowed[key] = struct{}{}
	}
	for key := range parameters {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%w: %q", ErrUnregisteredExternalParameter, key)
		}
	}
	return nil
}

func verifySourceDependency(dependencies []ResourceDescriptor, policy Policy) error {
	for _, dependency := range dependencies {
		if !digestContains(dependency.Digest, policy.SourceCommit) {
			continue
		}
		if policy.SourceRepository == "" || repositoryMatches(dependency.URI, policy.SourceRepository) {
			return nil
		}
	}
	if len(dependencies) == 0 {
		return fmt.Errorf("%w: no resolvedDependencies entries", ErrSourceCommitMissing)
	}
	return fmt.Errorf("%w: want %q", ErrSourceCommitMissing, policy.SourceCommit)
}

func digestContains(digest DigestSet, commit string) bool {
	if commit == "" {
		return false
	}
	for _, value := range digest {
		if value == commit {
			return true
		}
	}
	return false
}

func repositoryMatches(dependencyURI, trustedRepository string) bool {
	dependency := parseRepository(dependencyURI)
	trusted := parseRepository(trustedRepository)
	if dependency.path == "" || trusted.path == "" {
		return false
	}
	if trusted.host != "" {
		return dependency.host == trusted.host && dependency.path == trusted.path
	}
	return dependency.path == trusted.path ||
		strings.HasSuffix(dependency.path, "/"+trusted.path)
}

type repositoryRef struct {
	host string
	path string
}

func parseRepository(repository string) repositoryRef {
	repository = strings.TrimSpace(repository)
	repository = strings.TrimPrefix(repository, "git+")
	repository = strings.TrimSuffix(repository, "/")
	repository = strings.TrimSuffix(repository, ".git")

	if parsed, err := url.Parse(repository); err == nil && parsed.Host != "" {
		return repositoryRef{
			host: normalizeRepositoryHost(parsed.Host),
			path: normalizeRepositoryPath(parsed.Path),
		}
	}

	repository = strings.TrimPrefix(repository, "git@")
	if colon := strings.Index(repository, ":"); colon >= 0 {
		return repositoryRef{
			host: normalizeRepositoryHost(repository[:colon]),
			path: normalizeRepositoryPath(repository[colon+1:]),
		}
	}

	parts := strings.SplitN(repository, "/", 2)
	if len(parts) == 2 && strings.Contains(parts[0], ".") {
		return repositoryRef{
			host: normalizeRepositoryHost(parts[0]),
			path: normalizeRepositoryPath(parts[1]),
		}
	}
	return repositoryRef{path: normalizeRepositoryPath(repository)}
}

func normalizeRepositoryHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if at := strings.Index(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return host
}

func normalizeRepositoryPath(path string) string {
	path = strings.Trim(path, "/")
	if at := strings.Index(path, "@"); at >= 0 {
		path = path[:at]
	}
	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	return strings.ToLower(path)
}
