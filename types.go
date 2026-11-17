package netsec

import (
	"encoding/json"
	"errors"
)

// Image contains the exact bytes referenced by an OCI image manifest.
//
// Manifest must be the original, serialized manifest blob. Its digest is
// computed over these bytes exactly as they appear in the registry. Layers must
// contain the compressed layer blobs in the same order as manifest.layers.
type Image struct {
	Manifest []byte
	Config   []byte
	Layers   [][]byte
}

// Options controls optional admission-time checks.
type Options struct {
	// ExpectedManifestDigest is the digest the admission controller observed for
	// the deployed image (for example from a pod image reference). When set, it
	// must match the digest of Image.Manifest. A bare sha256 hex value is also
	// accepted.
	ExpectedManifestDigest string

	// MaxDecompressedLayerBytes limits the amount of data produced by a gzip
	// layer. Zero disables the limit.
	MaxDecompressedLayerBytes int64
}

// Subject is an in-toto statement subject.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Statement is the subset of an in-toto statement needed for image binding.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType,omitempty"`
	Predicate     json.RawMessage `json:"predicate,omitempty"`
}

// Result describes the digests that were checked.
type Result struct {
	ManifestDigest string
	ConfigDigest   string
	LayerDigests   []string
	DiffIDs        []string
	Subject        Subject
}

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Verification errors are wrapped with these sentinels so admission code can
// map verification failures to policy decisions with errors.Is.
var (
	ErrInvalidManifest      = errors.New("invalid OCI manifest")
	ErrInvalidConfig        = errors.New("invalid OCI image config")
	ErrInvalidDescriptor    = errors.New("invalid OCI descriptor")
	ErrManifestDigest       = errors.New("manifest digest mismatch")
	ErrConfigDigest         = errors.New("config digest mismatch")
	ErrLayerCount           = errors.New("layer count mismatch")
	ErrLayerDigest          = errors.New("layer compressed digest mismatch")
	ErrDiffID               = errors.New("layer decompressed digest mismatch")
	ErrUnsupportedMediaType = errors.New("unsupported layer media type")
	ErrInvalidGzipLayer     = errors.New("invalid gzip layer")
	ErrDecompressedTooLarge = errors.New("decompressed layer exceeds limit")
	ErrInvalidAttestation   = errors.New("invalid in-toto attestation")
	ErrSubjectMissing       = errors.New("attestation does not bind the deployed manifest")
)
