package netsec_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"example.org/netsec/q0131"
)

const (
	gzipLayerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
	tarLayerMediaType  = "application/vnd.oci.image.layer.v1.tar"
	statementV1        = "https://in-toto.io/Statement/v1"
	statementV01       = "https://in-toto.io/Statement/v0.1"
)

// imageFixture is a self-consistent OCI image plus a matching in-toto
// statement. Tests mutate individual fields to simulate each attack.
type imageFixture struct {
	manifest     *netsec.Manifest
	manifestRaw  []byte
	config       *netsec.ImageConfig
	configRaw    []byte
	statement    *netsec.Statement
	statementRaw []byte
	layers       [][]byte
	blobs        map[string][]byte
}

// tarLayer builds an uncompressed tar archive containing one text file.
func tarLayer(name, payload string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte(payload)
	header := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		panic(err)
	}
	if _, err := tw.Write(content); err != nil {
		panic(err)
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// gzipLayer compresses an uncompressed tar layer.
func gzipLayer(tarBytes []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(tarBytes); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestOf(data []byte) netsec.Digest {
	return netsec.Digest("sha256:" + hashHex(data))
}

func mustMarshal(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// jsonIndent re-encodes JSON with indentation, producing different raw
// bytes for the same document.
func jsonIndent(buf *bytes.Buffer, raw []byte) error {
	return json.Indent(buf, raw, "", "  ")
}

// buildFixture assembles a valid three-layer gzip image whose attestation
// subject is the manifest digest, plus a blob store keyed by digest.
func buildFixture() *imageFixture {
	tarBlobs := [][]byte{
		tarLayer("etc/os-release", "distro=test\nlayer=0\n"),
		tarLayer("app/bin/server", "#!/bin/sh\necho layer1\n"),
		tarLayer("app/config.yaml", "mode: production\nlayer: 2\n"),
	}

	layerDescriptors := make([]netsec.Descriptor, len(tarBlobs))
	compressedLayers := make([][]byte, len(tarBlobs))
	diffIDs := make([]netsec.Digest, len(tarBlobs))
	blobs := make(map[string][]byte)

	for i, tarBlob := range tarBlobs {
		diffID := digestOf(tarBlob)
		compressed := gzipLayer(tarBlob)
		compressedDigest := digestOf(compressed)

		diffIDs[i] = diffID
		compressedLayers[i] = compressed
		layerDescriptors[i] = netsec.Descriptor{
			MediaType: gzipLayerMediaType,
			Digest:    compressedDigest,
			Size:      int64(len(compressed)),
		}
		blobs[compressedDigest.String()] = compressed
	}

	config := &netsec.ImageConfig{
		Architecture: "arm64",
		OS:           "linux",
		RootFS: netsec.RootFS{
			Type:    "layers",
			DiffIDs: diffIDs,
		},
	}
	configRaw := mustMarshal(config)
	configDigest := digestOf(configRaw)
	blobs[configDigest.String()] = configRaw

	manifest := &netsec.Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config: netsec.Descriptor{
			MediaType: "application/vnd.oci.image.config.v1+json",
			Digest:    configDigest,
			Size:      int64(len(configRaw)),
		},
		Layers: layerDescriptors,
	}
	manifestRaw := mustMarshal(manifest)

	statement := statementFor(manifestRaw)
	statementRaw := mustMarshal(statement)

	return &imageFixture{
		manifest:     manifest,
		manifestRaw:  manifestRaw,
		config:       config,
		configRaw:    configRaw,
		statement:    statement,
		statementRaw: statementRaw,
		layers:       compressedLayers,
		blobs:        blobs,
	}
}

// statementFor builds an in-toto statement whose single subject is the
// sha256 digest of the supplied raw manifest.
func statementFor(rawManifest []byte) *netsec.Statement {
	return &netsec.Statement{
		Type: statementV1,
		Subject: []netsec.Subject{
			{
				Name:   "example.org/app:1.0.0",
				Digest: map[string]string{"sha256": hashHex(rawManifest)},
			},
		},
		PredicateType: "https://example.org/predicate/release/v1",
		Predicate:     map[string]any{"builder": "test"},
	}
}

// rebuildManifest re-serializes the mutated manifest and refreshes the raw
// bytes used by callers.
func (f *imageFixture) rebuildManifest() {
	f.manifestRaw = mustMarshal(f.manifest)
}

// rebuildStatement re-serializes the mutated attestation.
func (f *imageFixture) rebuildStatement() {
	f.statementRaw = mustMarshal(f.statement)
}

// replaceSubject points the attestation at an arbitrary digest.
func (f *imageFixture) replaceSubject(digest netsec.Digest) {
	f.statement.Subject = []netsec.Subject{
		{
			Name:   "example.org/attacker:9.9.9",
			Digest: map[string]string{"sha256": digest.Encoded()},
		},
	}
	f.rebuildStatement()
}

// mapGetter adapts the in-memory blob map to netsec.BlobGetter.
type mapGetter map[string][]byte

func (m mapGetter) GetBlob(_ context.Context, digest netsec.Digest) ([]byte, error) {
	blob, ok := m[digest.String()]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", digest)
	}
	return blob, nil
}
