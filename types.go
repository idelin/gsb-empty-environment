package netsec

import (
	"context"
	"encoding/json"
	"fmt"
)

// Descriptor is the subset of an OCI descriptor needed by the referrers
// filter. ArtifactType is the OCI 1.1 artifact type (for example, a signature
// or attestation media type).
type Descriptor struct {
	MediaType    string            `json:"mediaType,omitempty"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// Manifest is the subset of an OCI image manifest or artifact manifest used by
// the filter. Both Layers and Blobs are accepted because registries and older
// clients use both representations.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Config        *Descriptor       `json:"config,omitempty"`
	Layers        []Descriptor      `json:"layers,omitempty"`
	Blobs         []Descriptor      `json:"blobs,omitempty"`
	Manifests     []Descriptor      `json:"manifests,omitempty"`
	Subject       *Descriptor       `json:"subject,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Index is the subset of an OCI image index used by the legacy referrers
// fallback.
type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Manifests     []Descriptor      `json:"manifests"`
	Subject       *Descriptor       `json:"subject,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// ManifestFetcher returns the raw manifest bytes for an OCI digest or tag. The
// returned bytes are hashed by the filter; a caller-provided media type is not
// trusted.
type ManifestFetcher interface {
	FetchManifest(ctx context.Context, reference string) ([]byte, error)
}

// ManifestFetcherFunc adapts a function to ManifestFetcher.
type ManifestFetcherFunc func(ctx context.Context, reference string) ([]byte, error)

// FetchManifest implements ManifestFetcher.
func (f ManifestFetcherFunc) FetchManifest(ctx context.Context, reference string) ([]byte, error) {
	return f(ctx, reference)
}

// VerifyFunc receives only descriptors and manifests that passed every filter
// check.
type VerifyFunc func(ctx context.Context, descriptor Descriptor, manifest []byte) error

// Verifier is an alias for VerifyFunc.
type Verifier = VerifyFunc

// VerifierFunc is an alias for VerifyFunc.
type VerifierFunc = VerifyFunc

func parseManifest(content []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("netsec: decode manifest: %w", err)
	}
	return manifest, nil
}

func parseIndex(content []byte) (Index, error) {
	var index Index
	if err := json.Unmarshal(content, &index); err != nil {
		return Index{}, fmt.Errorf("netsec: decode fallback index: %w", err)
	}
	if index.SchemaVersion != 0 && index.SchemaVersion != 2 {
		return Index{}, fmt.Errorf("netsec: unsupported fallback index schema version %d", index.SchemaVersion)
	}
	return index, nil
}
