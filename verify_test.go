package netsec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fixture struct {
	image     Image
	statement []byte
	diffIDs   []string
}

func TestVerifyAcceptsBoundImage(t *testing.T) {
	f := newFixture(t, "release-a")

	result, err := Verify(f.image, f.statement)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.ManifestDigest != SHA256Digest(f.image.Manifest) {
		t.Fatalf("manifest digest = %s", result.ManifestDigest)
	}
	if result.ConfigDigest != SHA256Digest(f.image.Config) {
		t.Fatalf("config digest = %s", result.ConfigDigest)
	}
	if len(result.LayerDigests) != 2 || len(result.DiffIDs) != 2 {
		t.Fatalf("verified %d layers and %d diff IDs", len(result.LayerDigests), len(result.DiffIDs))
	}
	for index := range f.diffIDs {
		if result.DiffIDs[index] != f.diffIDs[index] {
			t.Fatalf("diff ID %d = %s, want %s", index, result.DiffIDs[index], f.diffIDs[index])
		}
	}
	if result.Subject.Name != "example.com/app:latest" {
		t.Fatalf("matched subject = %q", result.Subject.Name)
	}
}

func TestVerifyRejectsSwappedSubject(t *testing.T) {
	f := newFixture(t, "release-a")
	other := newFixture(t, "release-b")

	tests := []struct {
		name      string
		statement []byte
	}{
		{
			name:      "different manifest",
			statement: statementForDigest(t, digestHex(SHA256Digest(other.image.Manifest))),
		},
		{
			name:      "config digest used as subject",
			statement: statementForDigest(t, digestHex(SHA256Digest(f.image.Config))),
		},
		{
			name:      "layer digest used as subject",
			statement: statementForDigest(t, digestHex(SHA256Digest(f.image.Layers[0]))),
		},
		{
			name:      "attestation from another image",
			statement: other.statement,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Verify(f.image, test.statement); !errors.Is(err, ErrSubjectMissing) {
				t.Fatalf("Verify() error = %v, want ErrSubjectMissing", err)
			}
		})
	}
}

func TestVerifyAcceptsManifestDigestAmongMultipleSubjects(t *testing.T) {
	f := newFixture(t, "release-a")
	statement := statementWithSubjects(t,
		Subject{Name: "layer", Digest: map[string]string{"sha256": digestHex(SHA256Digest(f.image.Layers[0]))}},
		Subject{Name: "manifest", Digest: map[string]string{"sha256": digestHex(SHA256Digest(f.image.Manifest))}},
	)

	if _, err := Verify(f.image, statement); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifyRejectsMissingLayer(t *testing.T) {
	f := newFixture(t, "release-a")

	t.Run("blob omitted", func(t *testing.T) {
		image := f.image
		image.Layers = image.Layers[:1]
		if _, err := Verify(image, f.statement); !errors.Is(err, ErrLayerCount) {
			t.Fatalf("Verify() error = %v, want ErrLayerCount", err)
		}
	})

	t.Run("config and manifest disagree", func(t *testing.T) {
		manifest := encodeManifest(t, f.image.Config, f.image.Layers[:1])
		statement := statementForManifest(t, manifest, "example.com/app:latest")
		image := Image{Manifest: manifest, Config: f.image.Config, Layers: f.image.Layers[:1]}
		if _, err := Verify(image, statement); !errors.Is(err, ErrLayerCount) {
			t.Fatalf("Verify() error = %v, want ErrLayerCount", err)
		}
	})
}

func TestVerifyRejectsLayerOrderSwap(t *testing.T) {
	f := newFixture(t, "release-a")

	t.Run("blob order", func(t *testing.T) {
		image := f.image
		image.Layers = [][]byte{image.Layers[1], image.Layers[0]}
		if _, err := Verify(image, f.statement); !errors.Is(err, ErrLayerDigest) {
			t.Fatalf("Verify() error = %v, want ErrLayerDigest", err)
		}
	})

	t.Run("diff_ids order", func(t *testing.T) {
		config := encodeConfig(t, []string{f.diffIDs[1], f.diffIDs[0]})
		manifest := encodeManifest(t, config, f.image.Layers)
		statement := statementForManifest(t, manifest, "example.com/app:latest")
		image := Image{Manifest: manifest, Config: config, Layers: f.image.Layers}
		if _, err := Verify(image, statement); !errors.Is(err, ErrDiffID) {
			t.Fatalf("Verify() error = %v, want ErrDiffID", err)
		}
	})
}

func TestVerifyRejectsForgedGzip(t *testing.T) {
	f := newFixture(t, "release-a")
	valid := f.image.Layers[0]

	truncated := append([]byte(nil), valid[:len(valid)-8]...)
	badChecksum := append([]byte(nil), valid...)
	badChecksum[len(badChecksum)-8] ^= 0xff
	fakeHeader := []byte{0x1f, 0x8b, 0x08, 0x00, 0, 0, 0, 0, 0, 0xff, 0xde, 0xad, 0xbe, 0xef}
	plainTar := makeTar(t, map[string]string{"not-gzip.txt": "plain tar bytes"})

	tests := []struct {
		name  string
		layer []byte
	}{
		{name: "truncated trailer", layer: truncated},
		{name: "corrupt checksum", layer: badChecksum},
		{name: "fake gzip header", layer: fakeHeader},
		{name: "plain tar declared gzip", layer: plainTar},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layers := [][]byte{test.layer, f.image.Layers[1]}
			manifest := encodeManifest(t, f.image.Config, layers)
			statement := statementForManifest(t, manifest, "example.com/app:latest")
			image := Image{Manifest: manifest, Config: f.image.Config, Layers: layers}
			if _, err := Verify(image, statement); !errors.Is(err, ErrInvalidGzipLayer) {
				t.Fatalf("Verify() error = %v, want ErrInvalidGzipLayer", err)
			}
		})
	}
}

func TestVerifyRejectsConfigBlobMismatch(t *testing.T) {
	f := newFixture(t, "release-a")
	image := f.image
	image.Config = append(append([]byte(nil), f.image.Config...), '\n')
	if _, err := Verify(image, f.statement); !errors.Is(err, ErrConfigDigest) {
		t.Fatalf("Verify() error = %v, want ErrConfigDigest", err)
	}
}

func TestVerifyRejectsDecompressedDigestMismatch(t *testing.T) {
	f := newFixture(t, "release-a")
	replacementTar := makeTar(t, map[string]string{
		"etc/os-release": "ID=attacker\n",
	})
	replacementLayer := gzipBytes(t, replacementTar)
	layers := [][]byte{replacementLayer, f.image.Layers[1]}

	// The compressed descriptor and attestation are rebuilt to match the
	// replacement blob. Only rootfs.diff_ids still binds the original content.
	manifest := encodeManifest(t, f.image.Config, layers)
	statement := statementForManifest(t, manifest, "example.com/app:latest")
	image := Image{Manifest: manifest, Config: f.image.Config, Layers: layers}
	if _, err := Verify(image, statement); !errors.Is(err, ErrDiffID) {
		t.Fatalf("Verify() error = %v, want ErrDiffID", err)
	}
}

func TestVerifyExpectedManifestDigest(t *testing.T) {
	f := newFixture(t, "release-a")
	digest := SHA256Digest(f.image.Manifest)

	if _, err := VerifyWithOptions(f.image, f.statement, Options{ExpectedManifestDigest: digest}); err != nil {
		t.Fatalf("VerifyWithOptions() error = %v", err)
	}
	if _, err := VerifyWithOptions(f.image, f.statement, Options{ExpectedManifestDigest: digestHex(digest)}); err != nil {
		t.Fatalf("VerifyWithOptions() with bare hex error = %v", err)
	}

	other := newFixture(t, "release-b")
	_, err := VerifyWithOptions(f.image, f.statement, Options{
		ExpectedManifestDigest: SHA256Digest(other.image.Manifest),
	})
	if !errors.Is(err, ErrManifestDigest) {
		t.Fatalf("VerifyWithOptions() error = %v, want ErrManifestDigest", err)
	}
}

func TestVerifyAcceptsDSSEEnvelope(t *testing.T) {
	f := newFixture(t, "release-a")
	envelope := map[string]any{
		"payloadType": "application/vnd.in-toto+json",
		"payload":     base64.StdEncoding.EncodeToString(f.statement),
		"signatures": []any{
			map[string]string{"keyid": "test-key", "sig": "not-checked-by-this-package"},
		},
	}
	envelopeBytes := mustJSON(t, envelope)

	if _, err := Verify(f.image, envelopeBytes); err != nil {
		t.Fatalf("Verify() DSSE error = %v", err)
	}
}

func TestVerifyRejectsInvalidAttestation(t *testing.T) {
	f := newFixture(t, "release-a")
	if _, err := Verify(f.image, []byte(`{"subject":`)); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("Verify() error = %v, want ErrInvalidAttestation", err)
	}
	noType := mustJSON(t, map[string]any{
		"subject": []Subject{{
			Name:   "manifest",
			Digest: map[string]string{"sha256": digestHex(SHA256Digest(f.image.Manifest))},
		}},
	})
	if _, err := Verify(f.image, noType); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("Verify() without statement type error = %v, want ErrInvalidAttestation", err)
	}
}

func newFixture(t *testing.T, marker string) fixture {
	t.Helper()

	firstTar := makeTar(t, map[string]string{
		"etc/os-release": "ID=test\nVERSION=" + marker + "\n",
	})
	secondTar := makeTar(t, map[string]string{
		"app/payload.txt": "payload for " + marker + "\n",
	})
	diffIDs := []string{SHA256Digest(firstTar), SHA256Digest(secondTar)}

	layers := [][]byte{
		gzipBytes(t, firstTar),
		gzipBytes(t, secondTar),
	}
	config := encodeConfig(t, diffIDs)
	manifest := encodeManifest(t, config, layers)
	statement := statementForManifest(t, manifest, "example.com/app:latest")
	return fixture{
		image:     Image{Manifest: manifest, Config: config, Layers: layers},
		statement: statement,
		diffIDs:   diffIDs,
	}
}

type testDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func encodeManifest(t *testing.T, config []byte, layers [][]byte) []byte {
	t.Helper()

	layerDescriptors := make([]testDescriptor, len(layers))
	for index, layer := range layers {
		layerDescriptors[index] = testDescriptor{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    SHA256Digest(layer),
			Size:      int64(len(layer)),
		}
	}
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     ociManifestMediaType,
		"config": testDescriptor{
			MediaType: ociConfigMediaType,
			Digest:    SHA256Digest(config),
			Size:      int64(len(config)),
		},
		"layers": layerDescriptors,
	}
	return mustJSON(t, manifest)
}

func encodeConfig(t *testing.T, diffIDs []string) []byte {
	t.Helper()
	config := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": diffIDs,
		},
	}
	return mustJSON(t, config)
}

func statementForManifest(t *testing.T, manifest []byte, name string) []byte {
	t.Helper()
	return statementForDigestWithName(t, digestHex(SHA256Digest(manifest)), name)
}

func statementForDigest(t *testing.T, digest string) []byte {
	t.Helper()
	return statementForDigestWithName(t, digest, "example.com/app:latest")
}

func statementForDigestWithName(t *testing.T, digest, name string) []byte {
	t.Helper()
	return statementWithSubject(t, Subject{Name: name, Digest: map[string]string{"sha256": digest}})
}

func statementWithSubject(t *testing.T, subject Subject) []byte {
	t.Helper()
	return statementWithSubjects(t, subject)
}

func statementWithSubjects(t *testing.T, subjects ...Subject) []byte {
	t.Helper()
	statement := map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"subject":       subjects,
		"predicateType": "https://slsa.dev/provenance/v1",
		"predicate": map[string]any{
			"buildType": "https://example.test/build",
		},
	}
	return mustJSON(t, statement)
}

func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, contents := range files {
		data := []byte(contents)
		header := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatalf("write tar contents: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buffer.Bytes()
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip data: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buffer.Bytes()
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}

func TestDigestParsingIsStrict(t *testing.T) {
	if _, err := parseSHA256Digest("sha256:short"); err == nil {
		t.Fatal("parseSHA256Digest accepted a short digest")
	}
	if _, err := parseSHA256Digest("sha512:" + strings.Repeat("a", 128)); err == nil {
		t.Fatal("parseSHA256Digest accepted a non-sha256 digest")
	}
	if _, err := parseSHA256Digest(strings.Repeat("a", 64)); err != nil {
		t.Fatalf("parseSHA256Digest rejected bare sha256 hex: %v", err)
	}
}
