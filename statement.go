package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	statementTypeV1  = "https://in-toto.io/Statement/v1"
	statementTypeV01 = "https://in-toto.io/Statement/v0.1"
)

// Statement is the subset of an in-toto statement needed for admission
// binding.  Predicate is kept as raw JSON so that multiple provenance
// predicate versions can be supported without changing the wire format.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

// Subject is an in-toto subject and its content digest.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

func decodeStatement(payload []byte) (*Statement, error) {
	var statement Statement
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&statement); err != nil {
		return nil, fmt.Errorf("netsec: decode in-toto statement: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}

	switch statement.Type {
	case statementTypeV1, statementTypeV01:
	default:
		return nil, fmt.Errorf("netsec: unsupported in-toto statement type %q", statement.Type)
	}
	return &statement, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("netsec: trailing JSON value after document")
		}
		return fmt.Errorf("netsec: trailing data after JSON document: %w", err)
	}
	return nil
}

func (s *Statement) validateTarget(policy Policy) error {
	targetName, targetDigest, err := policy.effectiveTarget()
	if err != nil {
		return err
	}
	if len(s.Subject) != 1 {
		return fmt.Errorf("netsec: in-toto statement must have exactly one subject, got %d", len(s.Subject))
	}
	if targetName != "" && s.Subject[0].Name != targetName {
		return fmt.Errorf("netsec: subject name %q does not match target %q", s.Subject[0].Name, targetName)
	}
	if len(targetDigest) == 0 {
		return errors.New("netsec: policy target digest is empty")
	}

	actual := make(map[string]string, len(s.Subject[0].Digest))
	for algorithm, value := range s.Subject[0].Digest {
		normalisedAlgorithm := strings.ToLower(strings.TrimSpace(algorithm))
		if normalisedAlgorithm == "" {
			return errors.New("netsec: subject contains an empty digest algorithm")
		}
		if _, exists := actual[normalisedAlgorithm]; exists {
			return fmt.Errorf("netsec: subject contains duplicate digest algorithm %q", normalisedAlgorithm)
		}
		actual[normalisedAlgorithm] = normaliseDigestValue(value)
	}
	if len(actual) == 0 {
		return errors.New("netsec: subject has no content digest")
	}

	expected := make(map[string]string, len(targetDigest))
	for algorithm, value := range targetDigest {
		normalisedAlgorithm := strings.ToLower(strings.TrimSpace(algorithm))
		normalisedValue := normaliseDigestValue(value)
		if normalisedAlgorithm == "" || normalisedValue == "" {
			return fmt.Errorf("netsec: policy contains an invalid target digest for algorithm %q", algorithm)
		}
		expected[normalisedAlgorithm] = normalisedValue
	}
	for algorithm, value := range expected {
		got, ok := actual[algorithm]
		if !ok {
			return fmt.Errorf("netsec: subject digest is missing %s", algorithm)
		}
		if got != value {
			return fmt.Errorf("netsec: subject %s digest does not match target artifact", algorithm)
		}
	}
	return nil
}

func normaliseDigestValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexByte(value, ':'); separator >= 0 {
		switch value[:separator] {
		case "sha1", "sha256", "sha384", "sha512":
			value = value[separator+1:]
		}
	}
	return value
}

func (s *Statement) validatePolicyBinding(policy Policy) error {
	if expected := policy.expectedPredicateType(); expected != "" && s.PredicateType != expected {
		return fmt.Errorf("netsec: predicate type %q does not match policy %q", s.PredicateType, expected)
	}

	repository, commit, builder, err := extractPredicateBinding(s.Predicate)
	if err != nil {
		return err
	}
	repositoryPolicy := policy.Repository
	if repositoryPolicy == "" {
		repositoryPolicy = policy.ExpectedRepository
	}
	if repositoryPolicy == "" {
		return errors.New("netsec: policy has no repository")
	}
	if canonicalRepository(repository) != canonicalRepository(repositoryPolicy) {
		return fmt.Errorf("netsec: predicate repository %q does not match policy %q", repository, repositoryPolicy)
	}
	commitPolicy := policy.Commit
	if commitPolicy == "" {
		commitPolicy = policy.ExpectedCommit
	}
	if commitPolicy == "" {
		return errors.New("netsec: policy has no commit")
	}
	if normaliseCommit(commit) != normaliseCommit(commitPolicy) {
		return fmt.Errorf("netsec: predicate commit %q does not match policy %q", commit, commitPolicy)
	}
	expectedBuilder := policy.expectedBuilder()
	if expectedBuilder == "" {
		return errors.New("netsec: policy has no builder identity")
	}
	if strings.TrimSpace(builder) != strings.TrimSpace(expectedBuilder) {
		return fmt.Errorf("netsec: predicate builder %q does not match policy %q", builder, expectedBuilder)
	}
	return nil
}

type predicateBindings struct {
	repositories []string
	commits      []string
	builders     []string
}

func extractPredicateBinding(raw json.RawMessage) (string, string, string, error) {
	var predicate map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&predicate); err != nil {
		return "", "", "", fmt.Errorf("netsec: decode in-toto predicate: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return "", "", "", err
	}
	if predicate == nil {
		return "", "", "", errors.New("netsec: in-toto predicate is null")
	}

	var bindings predicateBindings
	for _, path := range [][]string{
		{"repository"},
		{"repo"},
		{"repositoryUri"},
		{"repositoryURI"},
		{"repositoryURL"},
		{"repositoryUrl"},
		{"sourceRepository"},
		{"source", "repository"},
		{"source", "repository", "uri"},
		{"source", "uri"},
		{"source", "url"},
		{"build", "repository"},
		{"build", "source", "repository"},
		{"provenance", "repository"},
		{"configSource", "uri"},
		{"configSource", "url"},
		{"configSource", "repository"},
		{"invocation", "configSource", "uri"},
		{"invocation", "configSource", "url"},
		{"invocation", "configSource", "repository"},
		{"buildDefinition", "externalParameters", "workflow", "repository"},
		{"buildDefinition", "externalParameters", "workflow", "repositoryUri"},
		{"buildDefinition", "externalParameters", "source", "repository"},
		{"buildDefinition", "externalParameters", "source", "uri"},
	} {
		if value, ok := lookupPath(predicate, path); ok {
			addRepository(&bindings, value)
		}
	}
	for _, path := range [][]string{
		{"commit"},
		{"commitSha"},
		{"commit_sha"},
		{"commitID"},
		{"commitId"},
		{"revision"},
		{"build", "commit"},
		{"provenance", "commit"},
		{"source", "commit"},
		{"source", "commitSha"},
		{"source", "revision"},
		{"source", "digest", "sha1"},
		{"source", "digest", "sha256"},
		{"configSource", "digest", "sha1"},
		{"configSource", "digest", "sha256"},
		{"invocation", "configSource", "digest", "sha1"},
		{"invocation", "configSource", "digest", "sha256"},
		{"buildDefinition", "externalParameters", "workflow", "sha"},
		{"buildDefinition", "externalParameters", "workflow", "commit"},
		{"buildDefinition", "externalParameters", "source", "commit"},
		{"buildDefinition", "externalParameters", "source", "digest", "sha1"},
		{"buildDefinition", "externalParameters", "source", "digest", "sha256"},
		{"repository", "commit"},
		{"source", "repository", "commit"},
	} {
		if value, ok := lookupPath(predicate, path); ok {
			addCommit(&bindings, value)
		}
	}
	for _, path := range [][]string{
		{"builder"},
		{"builderID"},
		{"builderId"},
		{"builderIdentity"},
		{"builder_id"},
		{"buildIdentity"},
		{"builder", "id"},
		{"builder", "uri"},
		{"builder", "identity"},
		{"build", "builder"},
		{"provenance", "builder"},
		{"repository", "builderIdentity"},
		{"runDetails", "builder", "id"},
		{"runDetails", "builder", "uri"},
		{"invocation", "builder", "id"},
		{"source", "builder"},
		{"repository", "builder"},
		{"buildDefinition", "externalParameters", "builder", "id"},
		{"buildDefinition", "externalParameters", "builder", "identity"},
		{"buildDefinition", "externalParameters", "builderIdentity"},
	} {
		if value, ok := lookupPath(predicate, path); ok {
			addBuilder(&bindings, value)
		}
	}

	repository, err := uniqueBinding("repository", bindings.repositories)
	if err != nil {
		return "", "", "", err
	}
	commit, err := uniqueBinding("commit", bindings.commits)
	if err != nil {
		return "", "", "", err
	}
	builder, err := uniqueBinding("builder", bindings.builders)
	if err != nil {
		return "", "", "", err
	}
	return repository, commit, builder, nil
}

func lookupPath(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := object[part]
		if !ok {
			return nil, false
		}
		current = value
	}
	return current, current != nil
}

func addRepository(bindings *predicateBindings, value any) {
	switch typed := value.(type) {
	case string:
		addCanonicalRepository(bindings, typed)
	case map[string]any:
		for _, key := range []string{"uri", "url", "name", "repository", "repo"} {
			if nested, ok := typed[key].(string); ok {
				addCanonicalRepository(bindings, nested)
			}
		}
	}
}

func addCanonicalRepository(bindings *predicateBindings, value string) {
	if canonical := canonicalRepository(value); canonical != "" {
		bindings.repositories = append(bindings.repositories, canonical)
	}
}

func addCommit(bindings *predicateBindings, value any) {
	switch typed := value.(type) {
	case string:
		addCanonicalCommit(bindings, typed)
	case map[string]any:
		for _, key := range []string{"sha1", "sha256", "commit", "sha", "revision"} {
			if nested, ok := typed[key].(string); ok {
				addCanonicalCommit(bindings, nested)
			}
		}
	}
}

func addCanonicalCommit(bindings *predicateBindings, value string) {
	if commit := normaliseCommit(value); commit != "" {
		bindings.commits = append(bindings.commits, commit)
	}
}

func addBuilder(bindings *predicateBindings, value any) {
	switch typed := value.(type) {
	case string:
		addCanonicalBuilder(bindings, typed)
	case map[string]any:
		for _, key := range []string{"id", "uri", "identity", "name"} {
			if nested, ok := typed[key].(string); ok {
				addCanonicalBuilder(bindings, nested)
			}
		}
	}
}

func addCanonicalBuilder(bindings *predicateBindings, value string) {
	if builder := strings.TrimSpace(value); builder != "" {
		bindings.builders = append(bindings.builders, builder)
	}
}

func uniqueBinding(name string, values []string) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("netsec: predicate is missing %s", name)
	}
	first := values[0]
	for _, value := range values[1:] {
		if value != first {
			return "", fmt.Errorf("netsec: predicate contains conflicting %s values %q and %q", name, first, value)
		}
	}
	return first, nil
}

func normaliseCommit(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexByte(value, ':'); separator >= 0 {
		switch value[:separator] {
		case "sha1", "sha256", "git":
			value = value[separator+1:]
		}
	}
	return value
}

func canonicalRepository(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "git+")

	if strings.HasPrefix(strings.ToLower(value), "git@") {
		value = value[4:]
		if separator := strings.IndexByte(value, ':'); separator >= 0 {
			value = value[:separator] + "/" + value[separator+1:]
		}
	}

	parseValue := value
	if !strings.Contains(parseValue, "://") {
		parseValue = "//" + parseValue
	}
	if parsed, err := url.Parse(parseValue); err == nil && parsed.Host != "" {
		path := strings.Trim(parsed.EscapedPath(), "/")
		// A source URI may carry the revision after an @, for example
		// git+https://github.com/acme/app@refs/heads/main.  The revision is
		// not part of repository identity.
		if separator := strings.LastIndexByte(path, '@'); separator >= 0 {
			path = path[:separator]
		}
		path = strings.TrimSuffix(path, ".git")
		path = strings.Trim(path, "/")

		host := strings.ToLower(parsed.Host)
		if path == "" {
			return host
		}
		return host + "/" + path
	}

	if separator := strings.LastIndexByte(value, '@'); separator >= 0 {
		value = value[:separator]
	}
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	return strings.TrimSuffix(value, "/")
}
