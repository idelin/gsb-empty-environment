package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"encoding/json"
)

// Sentinel errors let an admission controller map verification failures to a
// denial reason. Every error returned by this package wraps one of these, so
// errors.Is is always usable.
var (
	// ErrBadDigest indicates a malformed or unsupported digest value.
	ErrBadDigest = errors.New("netsec: invalid or unsupported digest")
	// ErrMalformedJSON indicates a manifest, config or statement that cannot
	// be parsed or that violates required structural constraints.
	ErrMalformedJSON = errors.New("netsec: malformed OCI or in-toto document")
	// ErrSubjectMismatch indicates that no in-toto subject names the exact
	// sha256 digest of the deployed manifest. This covers attestations
	// replayed from another image and subjects pointing at a layer or config
	// digest instead of the manifest digest.
	ErrSubjectMismatch = errors.New("netsec: attestation subject does not match manifest digest")
	// ErrConfigDigest indicates the config blob digest differs from the
	// descriptor embedded in the manifest.
	ErrConfigDigest = errors.New("netsec: config blob digest mismatch")
	// ErrConfigSize indicates the config blob size differs from its
	// descriptor.
	ErrConfigSize = errors.New("netsec: config blob size mismatch")
	// ErrLayerCount indicates the config rootfs diff_id count does not equal
	// the number of layers declared by the manifest (a missing layer).
	ErrLayerCount = errors.New("netsec: layer count does not match diff_ids")
	// ErrLayerDigest indicates a compressed layer blob digest differs from
	// the manifest descriptor (a swapped or replaced layer blob).
	ErrLayerDigest = errors.New("netsec: compressed layer digest mismatch")
	// ErrLayerSize indicates a layer blob size differs from its descriptor.
	ErrLayerSize = errors.New("netsec: layer blob size mismatch")
	// ErrDiffID indicates the sha256 of a decompressed layer does not equal
	// the corresponding rootfs.diff_ids entry, including layer reordering.
	ErrDiffID = errors.New("netsec: uncompressed layer diff_id mismatch")
	// ErrDecompress indicates a layer advertised as gzip cannot be
	// decompressed, for example a forged or truncated gzip stream.
	ErrDecompress = errors.New("netsec: layer decompression failed")
	// ErrUnsupportedLayer indicates a layer encoding this package does not
	// verify (currently anything other than gzip or an uncompressed tar).
	ErrUnsupportedLayer = errors.New("netsec: unsupported layer media type")
	// ErrBlobFetch indicates a BlobGetter could not supply a required blob.
	ErrBlobFetch = errors.New("netsec: blob fetch failed")
)

// unmarshalJSON rejects trailing data so attacker-appended bytes cannot be
// hidden behind a valid JSON document.
func unmarshalJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedJSON, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: trailing data after JSON document", ErrMalformedJSON)
	}
	return nil
}

// BlobGetter retrieves registry content blobs by digest. Admission
// controllers typically implement it over a containerd image store or a
// registry client.
type BlobGetter interface {
	GetBlob(ctx context.Context, digest Digest) ([]byte, error)
}

// BlobGetterFunc adapts a plain function to BlobGetter.
type BlobGetterFunc func(ctx context.Context, digest Digest) ([]byte, error)

// GetBlob implements BlobGetter.
func (f BlobGetterFunc) GetBlob(ctx context.Context, digest Digest) ([]byte, error) {
	return f(ctx, digest)
}

// ParseStatement decodes an in-toto attestation statement and validates its
// envelope and subject digests. Only sha256 subject digests are accepted.
func ParseStatement(raw []byte) (*Statement, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty statement", ErrMalformedJSON)
	}
	var stmt Statement
	if err := unmarshalJSON(raw, &stmt); err != nil {
		return nil, err
	}
	if !isInTotoStatement(stmt.Type) {
		return nil, fmt.Errorf("%w: unexpected statement type %q", ErrMalformedJSON, stmt.Type)
	}
	if len(stmt.Subject) == 0 {
		return nil, fmt.Errorf("%w: statement has no subjects", ErrSubjectMismatch)
	}
	for i := range stmt.Subject {
		if len(stmt.Subject[i].Digest) == 0 {
			return nil, fmt.Errorf("%w: subject %d has no digest set", ErrSubjectMismatch, i)
		}
		for algo, value := range stmt.Subject[i].Digest {
			if algo != "sha256" {
				return nil, fmt.Errorf("%w: subject %d uses unsupported algorithm %q", ErrBadDigest, i, algo)
			}
			if _, err := ParseDigest("sha256:" + value); err != nil {
				return nil, fmt.Errorf("%w: subject %d: %v", ErrBadDigest, i, err)
			}
		}
	}
	return &stmt, nil
}

func isInTotoStatement(typ string) bool {
	return typ == "https://in-toto.io/Statement/v0.1" ||
		typ == "https://in-toto.io/Statement/v1" ||
		typ == "in-toto/Statement"
}

// VerifyManifestSubject enforces that the in-toto statement's subject set
// contains the sha256 digest computed over the exact raw manifest bytes of
// the image being admitted.
//
// The digest is computed over the bytes as presented (not over a
// re-serialized document) so two semantically equal but byte-different
// manifests cannot share an attestation.
func VerifyManifestSubject(rawManifest []byte, stmt *Statement) error {
	if len(rawManifest) == 0 {
		return fmt.Errorf("%w: empty manifest", ErrMalformedJSON)
	}
	if stmt == nil {
		return fmt.Errorf("%w: nil statement", ErrSubjectMismatch)
	}
	want := SHA256Digest(rawManifest)
	for _, subject := range stmt.Subject {
		if value, ok := subject.Digest[want.Algorithm()]; ok {
			if "sha256:"+value == want.String() {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: manifest %s not found among %d subject(s)",
		ErrSubjectMismatch, want, len(stmt.Subject))
}

// ParseManifest decodes an OCI image manifest and validates that every
// descriptor carries a well-formed sha256 digest.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty manifest", ErrMalformedJSON)
	}
	var manifest Manifest
	if err := unmarshalJSON(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: unsupported schemaVersion %d",
			ErrMalformedJSON, manifest.SchemaVersion)
	}
	if manifest.Config.Digest.IsZero() {
		return nil, fmt.Errorf("%w: manifest has no config descriptor", ErrMalformedJSON)
	}
	if _, err := ParseDigest(manifest.Config.Digest.String()); err != nil {
		return nil, err
	}
	for i, layer := range manifest.Layers {
		if layer.Digest.IsZero() {
			return nil, fmt.Errorf("%w: layer %d has no digest", ErrMalformedJSON, i)
		}
		if _, err := ParseDigest(layer.Digest.String()); err != nil {
			return nil, fmt.Errorf("%w: layer %d: %v", ErrBadDigest, i, err)
		}
	}
	return &manifest, nil
}

// VerifyConfigBlob checks the config blob against its descriptor and returns
// the parsed image configuration, including the authoritative rootfs
// diff_ids.
func VerifyConfigBlob(blob []byte, config Descriptor) (*ImageConfig, error) {
	if _, err := verifySHA256(blob, config.Digest, ErrConfigDigest); err != nil {
		return nil, err
	}
	if config.Size > 0 && int64(len(blob)) != config.Size {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d",
			ErrConfigSize, config.Size, len(blob))
	}
	var imageConfig ImageConfig
	if err := unmarshalJSON(blob, &imageConfig); err != nil {
		return nil, err
	}
	if imageConfig.RootFS.Type != "layers" {
		return nil, fmt.Errorf("%w: rootfs.type is %q, want %q",
			ErrMalformedJSON, imageConfig.RootFS.Type, "layers")
	}
	for i, diffID := range imageConfig.RootFS.DiffIDs {
		if _, err := ParseDigest(diffID.String()); err != nil {
			return nil, fmt.Errorf("%w: diff_ids[%d]: %v", ErrBadDigest, i, err)
		}
	}
	return &imageConfig, nil
}

// VerifyLayers verifies every compressed layer blob against its manifest
// descriptor, decompresses each layer, and checks that the resulting
// uncompressed sha256 digests match rootfs.diff_ids one-to-one and in order.
//
// This simultaneously defeats:
//   - removed layers (the length check fails);
//   - reordered layers (per-position diff_id comparison fails);
//   - replaced or re-gzipped layer content (compressed digest or diff_id
//     fails);
//   - forged gzip streams (decompression fails explicitly).
func VerifyLayers(layerBlobs [][]byte, manifest *Manifest, imageConfig *ImageConfig) error {
	if manifest == nil || imageConfig == nil {
		return fmt.Errorf("%w: nil manifest or config", ErrMalformedJSON)
	}
	if len(layerBlobs) != len(manifest.Layers) {
		return fmt.Errorf("%w: %d blob(s) provided for %d descriptor(s)",
			ErrLayerCount, len(layerBlobs), len(manifest.Layers))
	}
	if len(manifest.Layers) != len(imageConfig.RootFS.DiffIDs) {
		return fmt.Errorf("%w: manifest declares %d layer(s) but config lists %d diff_id(s)",
			ErrLayerCount, len(manifest.Layers), len(imageConfig.RootFS.DiffIDs))
	}
	for i, descriptor := range manifest.Layers {
		blob := layerBlobs[i]
		if _, err := verifySHA256(blob, descriptor.Digest, ErrLayerDigest); err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
		if descriptor.Size > 0 && int64(len(blob)) != descriptor.Size {
			return fmt.Errorf("layer %d: %w: expected %d bytes, got %d",
				i, ErrLayerSize, descriptor.Size, len(blob))
		}

		diffID, err := decompressedDiffID(blob, descriptor.MediaType)
		if err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
		if diffID != imageConfig.RootFS.DiffIDs[i] {
			return fmt.Errorf("layer %d: %w: expected %s, got %s",
				i, ErrDiffID, imageConfig.RootFS.DiffIDs[i], diffID)
		}
	}
	return nil
}
