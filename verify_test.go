package netsec_test

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
	"testing"

	netsec "example.org/netsec/q0131"
)

type mapStore map[string][]byte

func (s mapStore) GetBlob(_ context.Context, digest string) (io.ReadCloser, error) {
	blob, ok := s[digest]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", digest)
	}
	return io.NopCloser(bytes.NewReader(blob)), nil
}

type testImage struct {
	manifest      netsec.Manifest
	manifestBytes []byte
	configBytes   []byte
	statement     *netsec.Statement
	store         mapStore
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func gzipData(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// buildImage creates a synthetic OCI image whose layers are the gzip
// compressed payloads, plus a matching in-toto statement.
func buildImage(t *testing.T, payloads ...[]byte) *testImage {
	t.Helper()
	store := mapStore{}
	var layers []netsec.Descriptor
	var diffIDs []string
	for _, payload := range payloads {
		compressed := gzipData(t, payload)
		digest := digestOf(compressed)
		store[digest] = compressed
		layers = append(layers, netsec.Descriptor{
			MediaType: netsec.MediaTypeLayerGzip,
			Digest:    digest,
			Size:      int64(len(compressed)),
		})
		diffIDs = append(diffIDs, digestOf(payload))
	}

	img := &testImage{store: store}
	img.configBytes = mustJSON(t, netsec.ImageConfig{
		RootFS: netsec.RootFS{Type: "layers", DiffIDs: diffIDs},
	})
	configDigest := digestOf(img.configBytes)
	store[configDigest] = img.configBytes

	img.manifest = netsec.Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config: netsec.Descriptor{
			MediaType: "application/vnd.oci.image.config.v1+json",
			Digest:    configDigest,
			Size:      int64(len(img.configBytes)),
		},
		Layers: layers,
	}
	img.resign(t)
	return img
}

// resign re-marshals the manifest and points the statement subject at the
// new manifest digest, isolating later checks from the subject check.
func (img *testImage) resign(t *testing.T) {
	t.Helper()
	img.manifestBytes = mustJSON(t, img.manifest)
	img.statement = &netsec.Statement{
		Type: "https://in-toto.io/Statement/v1",
		Subject: []netsec.Subject{{
			Name:   "registry.example/app",
			Digest: map[string]string{"sha256": strings.TrimPrefix(digestOf(img.manifestBytes), "sha256:")},
		}},
		PredicateType: "https://slsa.dev/provenance/v1",
	}
}

func (img *testImage) verify(t *testing.T) error {
	t.Helper()
	return netsec.VerifyImage(context.Background(), img.manifestBytes, img.configBytes, img.statement, img.store)
}

func TestVerifyImageOK(t *testing.T) {
	img := buildImage(t, []byte("layer-a"), []byte("layer-b"), []byte("layer-c"))
	if err := img.verify(t); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}
}

func TestVerifyImageSwappedSubject(t *testing.T) {
	deployed := buildImage(t, []byte("layer-a"))
	other := buildImage(t, []byte("attacker-image"))

	// Attacker presents the deployed manifest with an attestation issued
	// for a different image.
	deployed.statement = other.statement
	if err := deployed.verify(t); err == nil {
		t.Fatal("expected subject mismatch to be rejected")
	} else if !strings.Contains(err.Error(), "no in-toto subject") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyImageSubjectNamesLayerOrConfig(t *testing.T) {
	img := buildImage(t, []byte("layer-a"), []byte("layer-b"))

	for _, digest := range []string{
		img.manifest.Layers[0].Digest, // layer blob digest
		img.manifest.Config.Digest,    // config digest
	} {
		stmt := &netsec.Statement{
			Type:    "https://in-toto.io/Statement/v1",
			Subject: []netsec.Subject{{Digest: map[string]string{"sha256": strings.TrimPrefix(digest, "sha256:")}}},
		}
		if err := netsec.VerifyImage(context.Background(), img.manifestBytes, img.configBytes, stmt, img.store); err == nil {
			t.Fatalf("subject %s must not satisfy the manifest check", digest)
		}
	}
}

func TestVerifyImageMissingLayer(t *testing.T) {
	img := buildImage(t, []byte("layer-a"), []byte("layer-b"), []byte("layer-c"))

	// Manifest drops a layer while the config still declares three diff_ids.
	img.manifest.Layers = img.manifest.Layers[:2]
	img.resign(t)
	if err := img.verify(t); err == nil {
		t.Fatal("expected layer count mismatch to be rejected")
	} else if !strings.Contains(err.Error(), "diff_ids") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyImageLayerOrderSwapped(t *testing.T) {
	img := buildImage(t, []byte("layer-a"), []byte("layer-b"), []byte("layer-c"))

	img.manifest.Layers[0], img.manifest.Layers[1] = img.manifest.Layers[1], img.manifest.Layers[0]
	img.resign(t)
	if err := img.verify(t); err == nil {
		t.Fatal("expected swapped layer order to be rejected")
	} else if !strings.Contains(err.Error(), "diff_id mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyImageForgedGzip(t *testing.T) {
	t.Run("not a gzip stream", func(t *testing.T) {
		img := buildImage(t, []byte("layer-a"))

		// Attacker ships a non-gzip blob and updates the manifest to match.
		forged := []byte("this is not a gzip stream at all")
		digest := digestOf(forged)
		img.store[digest] = forged
		img.manifest.Layers[0].Digest = digest
		img.manifest.Layers[0].Size = int64(len(forged))
		img.resign(t)
		if err := img.verify(t); err == nil {
			t.Fatal("expected invalid gzip stream to be rejected")
		} else if !strings.Contains(err.Error(), "gzip") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("blob does not match descriptor", func(t *testing.T) {
		img := buildImage(t, []byte("layer-a"))

		// Registry serves bytes that differ from the manifest descriptor.
		img.store[img.manifest.Layers[0].Digest] = gzipData(t, []byte("tampered"))
		if err := img.verify(t); err == nil {
			t.Fatal("expected compressed digest mismatch to be rejected")
		} else if !strings.Contains(err.Error(), "compressed digest mismatch") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("decompressed content does not match diff_id", func(t *testing.T) {
		img := buildImage(t, []byte("layer-a"))

		// Valid gzip, correct compressed digest, wrong diff_id in config.
		var cfg netsec.ImageConfig
		if err := json.Unmarshal(img.configBytes, &cfg); err != nil {
			t.Fatal(err)
		}
		cfg.RootFS.DiffIDs[0] = digestOf([]byte("something-else"))
		img.configBytes = mustJSON(t, cfg)
		configDigest := digestOf(img.configBytes)
		img.store[configDigest] = img.configBytes
		img.manifest.Config.Digest = configDigest
		img.manifest.Config.Size = int64(len(img.configBytes))
		img.resign(t)
		if err := img.verify(t); err == nil {
			t.Fatal("expected diff_id mismatch to be rejected")
		} else if !strings.Contains(err.Error(), "diff_id mismatch") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestVerifyImageTamperedConfig(t *testing.T) {
	img := buildImage(t, []byte("layer-a"))

	// Config bytes no longer match the manifest's config descriptor.
	img.configBytes = mustJSON(t, netsec.ImageConfig{
		RootFS: netsec.RootFS{Type: "layers", DiffIDs: []string{digestOf([]byte("forged-diff-id"))}},
	})
	if err := img.verify(t); err == nil {
		t.Fatal("expected config digest mismatch to be rejected")
	} else if !strings.Contains(err.Error(), "config digest mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}
