package netsec

import (
	"bytes"
	"io"
)

const (
	// MediaTypeOCIIndex is the OCI multi-platform image index media type.
	MediaTypeOCIIndex = "application/vnd.oci.image.index.v1+json"
	// MediaTypeDockerList is the Docker multi-platform manifest list media type.
	MediaTypeDockerList = "application/vnd.docker.distribution.manifest.list.v2+json"
	// MediaTypeOCIManifest is the OCI single-platform image manifest media type.
	MediaTypeOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	// MediaTypeDockerManifest is the Docker schema 2 image manifest media type.
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	// MediaTypeOCIConfig is the OCI image config media type.
	MediaTypeOCIConfig = "application/vnd.oci.image.config.v1+json"
	// MediaTypeDockerConfig is the Docker image config media type.
	MediaTypeDockerConfig = "application/vnd.docker.container.image.v1+json"
	// MediaTypeOCILayer is the OCI gzip layer media type.
	MediaTypeOCILayer = "application/vnd.oci.image.layer.v1.tar+gzip"
	// MediaTypeDockerLayer is the Docker gzip layer media type.
	MediaTypeDockerLayer = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Platform is an OCI/Docker target platform.
type Platform struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
	OSVersion    string `json:"os.version,omitempty"`
}

// Descriptor identifies content by digest and describes its type and size.
type Descriptor struct {
	MediaType    string            `json:"mediaType,omitempty"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	URLs         []string          `json:"urls,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Platform     *Platform         `json:"platform,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
}

// Index is an OCI image index or Docker manifest list.
type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Manifests     []Descriptor      `json:"manifests"`
	Subject       *Descriptor       `json:"subject,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Manifest is an OCI image manifest or Docker schema 2 manifest.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	ArtifactType  string            `json:"artifactType,omitempty"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Subject       *Descriptor       `json:"subject,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// RootFSConfig is the rootfs section of an image config.
type RootFSConfig struct {
	Type    string   `json:"type,omitempty"`
	DiffIDs []string `json:"diff_ids,omitempty"`
}

// ImageConfig contains the fields needed to bind an image config to its platform
// and layers.
type ImageConfig struct {
	Architecture string        `json:"architecture,omitempty"`
	OS           string        `json:"os,omitempty"`
	Variant      string        `json:"variant,omitempty"`
	OSVersion    string        `json:"os.version,omitempty"`
	RootFS       *RootFSConfig `json:"rootfs,omitempty"`
}

// VerifiedBlob is content whose digest and size matched its descriptor.
type VerifiedBlob struct {
	Descriptor Descriptor
	Content    []byte
}

// Reader returns a fresh reader over the verified content.
func (blob VerifiedBlob) Reader() io.Reader {
	return bytes.NewReader(blob.Content)
}

// ResolvedImage is a complete, digest-verified platform selection.
type ResolvedImage struct {
	Reference          string
	RootDigest         Digest
	ManifestDigest     Digest
	ManifestDescriptor Descriptor
	Manifest           Manifest
	ManifestContent    []byte
	Platform           Platform
	Config             ImageConfig
	ConfigBlob         VerifiedBlob
	Layers             []VerifiedBlob
}

// Digest returns the selected image manifest digest.
func (image *ResolvedImage) Digest() Digest {
	return image.ManifestDigest
}
