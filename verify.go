package netsec

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ociManifestMediaType    = "application/vnd.oci.image.manifest.v1+json"
	dockerManifestMediaType = "application/vnd.docker.distribution.manifest.v2+json"

	ociConfigMediaType    = "application/vnd.oci.image.config.v1+json"
	dockerConfigMediaType = "application/vnd.docker.container.image.v1+json"
)

type manifestDocument struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

type configDocument struct {
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// Verify checks that an in-toto attestation binds the exact deployed manifest
// and that every layer is consistent with both the manifest and the image
// config.
//
// The image argument must contain the exact manifest bytes and the compressed
// layer blobs in manifest order. The attestation may be a raw in-toto statement
// or a DSSE envelope containing one.
func Verify(image Image, attestation []byte) (*Result, error) {
	return VerifyWithOptions(image, attestation, Options{})
}

// VerifyWithOptions is Verify with explicit admission-time options.
func VerifyWithOptions(image Image, attestation []byte, options Options) (*Result, error) {
	manifest, err := parseManifest(image.Manifest)
	if err != nil {
		return nil, err
	}

	manifestDigest := SHA256Digest(image.Manifest)
	if options.ExpectedManifestDigest != "" {
		expected, err := parseSHA256Digest(options.ExpectedManifestDigest)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid expected digest: %v", ErrManifestDigest, err)
		}
		if expected != manifestDigest {
			return nil, fmt.Errorf("%w: expected %s, got %s", ErrManifestDigest, expected, manifestDigest)
		}
	}

	statement, err := ParseStatement(attestation)
	if err != nil {
		return nil, err
	}
	subject, err := findManifestSubject(statement, manifestDigest)
	if err != nil {
		return nil, err
	}

	configDigest, err := verifyDescriptorBlob(manifest.Config, image.Config, ErrConfigDigest)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	config, err := parseConfig(image.Config)
	if err != nil {
		return nil, err
	}
	diffIDs, err := parseDiffIDs(config.RootFS.DiffIDs)
	if err != nil {
		return nil, err
	}
	if len(diffIDs) != len(manifest.Layers) {
		return nil, fmt.Errorf(
			"%w: config has %d diff_ids but manifest has %d layers",
			ErrLayerCount,
			len(diffIDs),
			len(manifest.Layers),
		)
	}
	if len(image.Layers) != len(manifest.Layers) {
		return nil, fmt.Errorf(
			"%w: manifest has %d layers but %d compressed blobs were supplied",
			ErrLayerCount,
			len(manifest.Layers),
			len(image.Layers),
		)
	}

	result := &Result{
		ManifestDigest: manifestDigest,
		ConfigDigest:   configDigest,
		LayerDigests:   make([]string, len(manifest.Layers)),
		DiffIDs:        make([]string, len(manifest.Layers)),
		Subject:        subject,
	}
	for index := range manifest.Layers {
		descriptor := manifest.Layers[index]
		compressed := image.Layers[index]

		layerDigest, err := verifyDescriptorBlob(descriptor, compressed, ErrLayerDigest)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", index, err)
		}
		diffID, err := layerDiffID(descriptor, compressed, options.MaxDecompressedLayerBytes)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", index, err)
		}
		if diffID != diffIDs[index] {
			return nil, fmt.Errorf(
				"%w at layer %d: config has %s, decompressed layer is %s",
				ErrDiffID,
				index,
				diffIDs[index],
				diffID,
			)
		}
		result.LayerDigests[index] = layerDigest
		result.DiffIDs[index] = diffID
	}
	return result, nil
}

func parseManifest(data []byte) (*manifestDocument, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty manifest", ErrInvalidManifest)
	}
	var manifest manifestDocument
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if manifest.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: schemaVersion is %d, want 2", ErrInvalidManifest, manifest.SchemaVersion)
	}
	switch strings.ToLower(strings.TrimSpace(manifest.MediaType)) {
	case "", ociManifestMediaType, dockerManifestMediaType:
	default:
		return nil, fmt.Errorf("%w: media type %q", ErrUnsupportedMediaType, manifest.MediaType)
	}
	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("%w: missing config descriptor", ErrInvalidManifest)
	}
	switch strings.ToLower(strings.TrimSpace(manifest.Config.MediaType)) {
	case "", ociConfigMediaType, dockerConfigMediaType:
	default:
		return nil, fmt.Errorf("%w: config media type %q", ErrUnsupportedMediaType, manifest.Config.MediaType)
	}
	return &manifest, nil
}

func parseConfig(data []byte) (*configDocument, error) {
	var config configDocument
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if config.RootFS.Type != "" && config.RootFS.Type != "layers" {
		return nil, fmt.Errorf("%w: rootfs.type is %q, want layers", ErrInvalidConfig, config.RootFS.Type)
	}
	return &config, nil
}

func parseDiffIDs(values []string) ([]string, error) {
	diffIDs := make([]string, len(values))
	for index, value := range values {
		digest, err := parseSHA256Digest(value)
		if err != nil {
			return nil, fmt.Errorf("%w: diff_ids[%d]: %v", ErrInvalidConfig, index, err)
		}
		diffIDs[index] = digest
	}
	return diffIDs, nil
}

func verifyDescriptorBlob(descriptor Descriptor, data []byte, mismatch error) (string, error) {
	digest, err := parseSHA256Digest(descriptor.Digest)
	if err != nil {
		return "", fmt.Errorf("%w: %v", mismatch, err)
	}
	if descriptor.Size < 0 {
		return "", fmt.Errorf("%w: negative size %d", mismatch, descriptor.Size)
	}
	if descriptor.Size != int64(len(data)) {
		return "", fmt.Errorf(
			"%w: descriptor size %d does not match blob size %d",
			mismatch,
			descriptor.Size,
			len(data),
		)
	}
	actual := SHA256Digest(data)
	if actual != digest {
		return "", fmt.Errorf("%w: descriptor has %s, blob is %s", mismatch, digest, actual)
	}
	return actual, nil
}
