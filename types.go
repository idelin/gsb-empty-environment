package netsec

// Descriptor is the OCI content descriptor used in image manifests.
type Descriptor struct {
	// MediaType describes the content type of the referenced blob.
	MediaType string `json:"mediaType,omitempty"`
	// Digest is the sha256 digest of the blob as stored in the registry.
	Digest Digest `json:"digest"`
	// Size is the blob size in bytes. A zero value means the size was not
	// declared and is therefore not checked.
	Size int64 `json:"size,omitempty"`
}

// Manifest is the OCI image manifest (schema version 2).
//
// See https://github.com/opencontainers/image-spec/blob/main/manifest.md.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// RootFS is the rootfs section of an OCI image configuration blob.
type RootFS struct {
	// Type must be the literal "layers".
	Type string `json:"type"`
	// DiffIDs holds, in order, the sha256 digests of the uncompressed
	// (tar) content of every image layer.
	DiffIDs []Digest `json:"diff_ids"`
}

// ImageConfig is the subset of an OCI image configuration blob that is
// relevant for verification. Additional fields are accepted and ignored.
//
// See https://github.com/opencontainers/image-spec/blob/main/config.md.
type ImageConfig struct {
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
	RootFS       RootFS `json:"rootfs"`
}

// Subject is one entry of an in-toto statement subject set.
type Subject struct {
	// Name is an opaque locator for the referenced software; verification
	// relies on Digest, not on Name, so an attacker cannot gain anything by
	// reusing a plausible name with a foreign digest.
	Name   string            `json:"name,omitempty"`
	Digest map[string]string `json:"digest"`
}

// Statement is an in-toto attestation statement (spec v0.1 and v1 share the
// same envelope). Predicate content is opaque to image verification.
//
// See https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     any       `json:"predicate,omitempty"`
}
