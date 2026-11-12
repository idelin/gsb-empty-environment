package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	// StatementType is the in-toto statement media type used by SLSA v1.
	StatementType = "https://in-toto.io/Statement/v1"
	// PredicateType is the SLSA v1 provenance predicate type.
	PredicateType = "https://slsa.dev/provenance/v1"

	// StatementTypeV1 and ProvenancePredicateTypeV1 are descriptive aliases.
	StatementTypeV1           = StatementType
	ProvenancePredicateTypeV1 = PredicateType
)

// Statement is an in-toto statement carrying a SLSA v1 provenance predicate.
//
// RawMessage is used for parameter objects so that field presence and the
// exact JSON shape can be checked before decoding. In particular, a present
// but empty internalParameters object is still a rejection.
type Statement struct {
	Type          string               `json:"_type"`
	Subject       []ResourceDescriptor `json:"subject,omitempty"`
	PredicateType string               `json:"predicateType"`
	Predicate     Predicate            `json:"predicate"`
}

// Predicate is the SLSA v1 provenance predicate.
type Predicate struct {
	BuildDefinition BuildDefinition `json:"buildDefinition"`
	RunDetails      RunDetails      `json:"runDetails"`
}

// BuildDefinition describes how the artifact was built.
type BuildDefinition struct {
	BuildType            string               `json:"buildType"`
	ExternalParameters   json.RawMessage      `json:"externalParameters,omitempty"`
	InternalParameters   json.RawMessage      `json:"internalParameters,omitempty"`
	ResolvedDependencies []ResourceDescriptor `json:"resolvedDependencies,omitempty"`
}

// RunDetails describes the builder and the build invocation.
type RunDetails struct {
	Builder    Builder              `json:"builder"`
	Metadata   json.RawMessage      `json:"metadata,omitempty"`
	Byproducts []ResourceDescriptor `json:"byproducts,omitempty"`
}

// Builder identifies the build service that produced the provenance.
type Builder struct {
	ID                  string               `json:"id"`
	Version             map[string]string    `json:"version,omitempty"`
	BuilderDependencies []ResourceDescriptor `json:"builderDependencies,omitempty"`
}

// ResourceDescriptor identifies an artifact or source dependency.
type ResourceDescriptor struct {
	Name             string         `json:"name,omitempty"`
	URI              string         `json:"uri,omitempty"`
	Digest           DigestSet      `json:"digest,omitempty"`
	Content          string         `json:"content,omitempty"`
	DownloadLocation string         `json:"downloadLocation,omitempty"`
	MediaType        string         `json:"mediaType,omitempty"`
	Annotations      map[string]any `json:"annotations,omitempty"`
}

// DigestSet maps a digest algorithm or hint to its value.
type DigestSet map[string]string

// ParseStatement decodes one SLSA v1 statement from JSON.
//
// Unknown fields are ignored; verification is driven by the fields that affect
// admission decisions.
func ParseStatement(data []byte) (*Statement, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: statement must be a JSON object", ErrInvalidStatement)
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var statement Statement
	if err := dec.Decode(&statement); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStatement, err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStatement, err)
	}
	return &statement, nil
}

// UnmarshalStatement is an alias for ParseStatement.
func UnmarshalStatement(data []byte) (*Statement, error) {
	return ParseStatement(data)
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("unexpected data after statement")
	}
	return err
}
