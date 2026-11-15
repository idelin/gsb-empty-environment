package netsec

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// OCI and Docker layer media types understood by this package.
const (
	MediaTypeLayerTar        = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeLayerGzip       = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeDockerLayerTar  = "application/vnd.docker.image.rootfs.diff.tar"
	MediaTypeDockerLayerGzip = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Manifest is an OCI image manifest.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// ImageConfig is the OCI image configuration document.
type ImageConfig struct {
	RootFS RootFS `json:"rootfs"`
}

// RootFS describes the layer stack of an image.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

// Subject is an in-toto statement subject entry.
type Subject struct {
	Name   string            `json:"name,omitempty"`
	Digest map[string]string `json:"digest"`
}

// Statement is an in-toto attestation statement.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType,omitempty"`
	Predicate     json.RawMessage `json:"predicate,omitempty"`
}

// BlobStore fetches content-addressable blobs such as image layers.
type BlobStore interface {
	GetBlob(ctx context.Context, digest string) (io.ReadCloser, error)
}

// decodeSHA256 validates a "sha256:<hex>" digest and returns the raw bytes.
func decodeSHA256(digest string) ([]byte, error) {
	algo, value, ok := strings.Cut(digest, ":")
	if !ok || algo != "sha256" {
		return nil, fmt.Errorf("unsupported digest %q: only sha256 is accepted", digest)
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("malformed sha256 digest %q", digest)
	}
	return raw, nil
}

// VerifySubject ensures the in-toto statement's subject set contains the
// sha256 digest of the deployed image manifest. This rejects attestations
// recycled from other images and statements whose subject names a layer or
// config digest instead of the manifest.
func VerifySubject(stmt *Statement, manifestDigest string) error {
	if stmt == nil {
		return fmt.Errorf("nil in-toto statement")
	}
	want, err := decodeSHA256(manifestDigest)
	if err != nil {
		return fmt.Errorf("invalid manifest digest: %w", err)
	}
	for _, sub := range stmt.Subject {
		value, ok := sub.Digest["sha256"]
		if !ok {
			continue
		}
		got, err := hex.DecodeString(value)
		if err != nil {
			continue
		}
		if bytes.Equal(got, want) {
			return nil
		}
	}
	return fmt.Errorf("no in-toto subject matches deployed manifest digest %s", manifestDigest)
}

// VerifyLayers checks that manifest layers and config diff_ids correspond
// one-to-one: every layer blob must hash to its descriptor digest, and its
// decompressed content must hash to the diff_id at the same position.
func VerifyLayers(ctx context.Context, m *Manifest, cfg *ImageConfig, store BlobStore) error {
	if len(m.Layers) != len(cfg.RootFS.DiffIDs) {
		return fmt.Errorf("manifest declares %d layers but config declares %d diff_ids",
			len(m.Layers), len(cfg.RootFS.DiffIDs))
	}
	for i := range m.Layers {
		if err := verifyLayer(ctx, store, m.Layers[i], cfg.RootFS.DiffIDs[i]); err != nil {
			return fmt.Errorf("layer %d (%s): %w", i, m.Layers[i].Digest, err)
		}
	}
	return nil
}

func verifyLayer(ctx context.Context, store BlobStore, desc Descriptor, wantDiffID string) error {
	wantCompressed, err := decodeSHA256(desc.Digest)
	if err != nil {
		return err
	}
	rc, err := store.GetBlob(ctx, desc.Digest)
	if err != nil {
		return fmt.Errorf("fetching blob: %w", err)
	}
	defer rc.Close()
	compressed, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("reading blob: %w", err)
	}
	if sum := sha256.Sum256(compressed); !bytes.Equal(sum[:], wantCompressed) {
		return fmt.Errorf("compressed digest mismatch: got sha256:%x, want %s", sum, desc.Digest)
	}

	var raw io.Reader = bytes.NewReader(compressed)
	switch desc.MediaType {
	case MediaTypeLayerTar, MediaTypeDockerLayerTar:
	case MediaTypeLayerGzip, MediaTypeDockerLayerGzip:
		gz, err := gzip.NewReader(raw)
		if err != nil {
			return fmt.Errorf("invalid gzip stream: %w", err)
		}
		defer gz.Close()
		raw = gz
	default:
		return fmt.Errorf("unsupported layer media type %q", desc.MediaType)
	}

	wantDiff, err := decodeSHA256(wantDiffID)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, raw); err != nil {
		return fmt.Errorf("decompressing layer: %w", err)
	}
	if !bytes.Equal(h.Sum(nil), wantDiff) {
		return fmt.Errorf("diff_id mismatch: got sha256:%x, want %s", h.Sum(nil), wantDiffID)
	}
	return nil
}

// VerifyImage performs the full admission check for a deployed image: the
// attestation subject must reference the manifest digest, the config blob
// must match the manifest's config descriptor, and the layer stack must
// match the config diff_ids.
func VerifyImage(ctx context.Context, manifestBytes, configBytes []byte, stmt *Statement, store BlobStore) error {
	manifestSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])
	if err := VerifySubject(stmt, manifestDigest); err != nil {
		return err
	}

	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("parsing manifest: %w", err)
	}

	wantConfig, err := decodeSHA256(m.Config.Digest)
	if err != nil {
		return fmt.Errorf("invalid config descriptor: %w", err)
	}
	if sum := sha256.Sum256(configBytes); !bytes.Equal(sum[:], wantConfig) {
		return fmt.Errorf("config digest mismatch: got sha256:%x, want %s", sum, m.Config.Digest)
	}

	var cfg ImageConfig
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return fmt.Errorf("parsing image config: %w", err)
	}
	return VerifyLayers(ctx, &m, &cfg, store)
}
