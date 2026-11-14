package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestResolveLegalMultiArchImage(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	amd64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("amd64 layer"))
	arm64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm64",
		Variant:      "v8",
	}, []byte("arm64 layer"))
	root := addTestIndex(t, source, "sha256", []Descriptor{amd64, arm64})
	mustTag(t, source, "example/app:latest", root)

	resolver := NewResolver(source)
	image, err := resolver.Resolve(context.Background(), "example/app:latest", Platform{
		OS:           "linux",
		Architecture: "arm64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.String() != arm64.Digest {
		t.Fatalf("ManifestDigest = %s, want %s", image.ManifestDigest, arm64.Digest)
	}
	if image.Platform.Architecture != "arm64" {
		t.Fatalf("Platform.Architecture = %q, want arm64", image.Platform.Architecture)
	}
	if len(image.Layers) != 1 {
		t.Fatalf("len(Layers) = %d, want 1", len(image.Layers))
	}
	if string(image.Layers[0].Content) != "arm64 layer" {
		t.Fatalf("layer content = %q", image.Layers[0].Content)
	}
	if image.ConfigBlob.Descriptor.Digest != image.Manifest.Config.Digest {
		t.Fatalf("config descriptor = %s, want %s", image.ConfigBlob.Descriptor.Digest, image.Manifest.Config.Digest)
	}
	if image.RootDigest.String() != root.String() {
		t.Fatalf("RootDigest = %s, want %s", image.RootDigest, root)
	}
}

func TestTagMoveDoesNotReuseOldRoot(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	firstChild := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("first layer"))
	firstRoot := addTestIndex(t, source, "sha256", []Descriptor{firstChild})
	mustTag(t, source, "example/app:latest", firstRoot)

	resolver := NewResolver(source)
	first, err := resolver.Resolve(context.Background(), "example/app:latest", Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}

	secondChild := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("second layer"))
	secondRoot := addTestIndex(t, source, "sha256", []Descriptor{secondChild})
	mustTag(t, source, "example/app:latest", secondRoot)

	second, err := resolver.Resolve(context.Background(), "example/app:latest", Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if first.ManifestDigest == second.ManifestDigest {
		t.Fatalf("tag move returned cached old child %s", first.ManifestDigest)
	}
	if first.ManifestDigest.String() != firstChild.Digest {
		t.Fatalf("old result changed to %s, want %s", first.ManifestDigest, firstChild.Digest)
	}
	if second.ManifestDigest.String() != secondChild.Digest {
		t.Fatalf("new result = %s, want %s", second.ManifestDigest, secondChild.Digest)
	}
}

func TestChildDigestMismatchFails(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	child := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("original layer"))
	root := addTestIndex(t, source, "sha256", []Descriptor{child})
	mustTag(t, source, "example/app:latest", root)
	mustPutBlob(t, source, child.Digest, []byte("tampered child"))

	resolver := NewResolver(source)
	_, err := resolver.Resolve(context.Background(), "example/app:latest", Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Resolve() error = %v, want ErrDigestMismatch", err)
	}
}

func TestAnnotationsCannotSubstituteForDigest(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	child := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("verified layer"))
	claimedDigest := child.Digest
	child.Digest = digestOf(t, "sha256", []byte("different manifest")).String()
	child.Annotations = map[string]string{
		"org.opencontainers.image.digest": claimedDigest,
		"vnd.docker.reference.digest":     claimedDigest,
	}
	root := addTestIndex(t, source, "sha256", []Descriptor{child})
	mustTag(t, source, "example/app:latest", root)

	manifestContent := map[string]any{
		"schemaVersion": 2,
		"mediaType":     MediaTypeOCIManifest,
	}
	manifestBytes := mustJSON(t, manifestContent)
	mustPutBlob(t, source, child.Digest, manifestBytes)

	resolver := NewResolver(source)
	_, err := resolver.Resolve(context.Background(), "example/app:latest", Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Resolve() error = %v, want ErrDigestMismatch", err)
	}
}

func TestVariantSelectionIsExact(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	armV6 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v6",
	}, []byte("arm v6"))
	armV7 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v7",
	}, []byte("arm v7"))
	root := addTestIndex(t, source, "sha256", []Descriptor{armV6, armV7})

	resolver := NewResolver(source)
	image, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v7",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.String() != armV7.Digest {
		t.Fatalf("selected %s, want v7 child %s", image.ManifestDigest, armV7.Digest)
	}

	_, err = resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v5",
	})
	if !errors.Is(err, ErrPlatformNotFound) {
		t.Fatalf("variant v5 error = %v, want ErrPlatformNotFound", err)
	}
}

func TestARM64V8AliasAndDuplicatePlatform(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	genericARM64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm64",
	}, []byte("generic arm64"))
	v8ARM64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm64",
		Variant:      "v8",
	}, []byte("v8 arm64"))

	resolver := NewResolver(source)
	validRoot := addTestIndex(t, source, "sha256", []Descriptor{genericARM64})
	image, err := resolver.Resolve(context.Background(), validRoot.String(), Platform{
		OS:           "linux",
		Architecture: "aarch64",
		Variant:      "8",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.String() != genericARM64.Digest {
		t.Fatalf("selected %s, want %s", image.ManifestDigest, genericARM64.Digest)
	}

	duplicateRoot := addTestIndex(t, source, "sha256", []Descriptor{genericARM64, v8ARM64})
	_, err = resolver.Resolve(context.Background(), duplicateRoot.String(), Platform{
		OS:           "linux",
		Architecture: "arm64",
	})
	if !errors.Is(err, ErrDuplicatePlatform) {
		t.Fatalf("duplicate platform error = %v, want ErrDuplicatePlatform", err)
	}
}

func TestOSVersionSelectionAndAmbiguity(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	windows10 := addTestImage(t, source, "sha256", Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.17763.3532",
	}, []byte("windows 10"))
	windows11 := addTestImage(t, source, "sha256", Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.20348.1547",
	}, []byte("windows 11"))
	root := addTestIndex(t, source, "sha256", []Descriptor{windows10, windows11})

	resolver := NewResolver(source)
	image, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.20348.1547",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.String() != windows11.Digest {
		t.Fatalf("selected %s, want %s", image.ManifestDigest, windows11.Digest)
	}

	_, err = resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "windows",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrAmbiguousPlatform) {
		t.Fatalf("ambiguous OS version error = %v, want ErrAmbiguousPlatform", err)
	}
}

func TestLayerTamperAndMissingLayerFail(t *testing.T) {
	t.Parallel()

	t.Run("tampered", func(t *testing.T) {
		source := NewMemorySource()
		child := addTestImage(t, source, "sha256", Platform{
			OS:           "linux",
			Architecture: "amd64",
		}, []byte("layer to tamper"))
		root := addTestIndex(t, source, "sha256", []Descriptor{child})
		manifest, config := loadTestManifestAndConfig(t, source, child)
		_ = config
		mustPutBlob(t, source, manifest.Layers[0].Digest, []byte("tampered layer"))

		resolver := NewResolver(source)
		_, err := resolver.Resolve(context.Background(), root.String(), Platform{
			OS:           "linux",
			Architecture: "amd64",
		})
		if !errors.Is(err, ErrDigestMismatch) || !errors.Is(err, ErrMissingLayer) {
			t.Fatalf("Resolve() error = %v, want ErrDigestMismatch and ErrMissingLayer", err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		source := NewMemorySource()
		child := addTestImage(t, source, "sha256", Platform{
			OS:           "linux",
			Architecture: "amd64",
		}, []byte("layer to remove"))
		root := addTestIndex(t, source, "sha256", []Descriptor{child})
		manifest, _ := loadTestManifestAndConfig(t, source, child)
		source.DeleteBlob(manifest.Layers[0].Digest)

		resolver := NewResolver(source)
		_, err := resolver.Resolve(context.Background(), root.String(), Platform{
			OS:           "linux",
			Architecture: "amd64",
		})
		if !errors.Is(err, ErrBlobNotFound) || !errors.Is(err, ErrMissingLayer) {
			t.Fatalf("Resolve() error = %v, want ErrBlobNotFound and ErrMissingLayer", err)
		}
	})
}

func TestDigestAlgorithmMismatchFailsByDefault(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	child := addTestImage(t, source, "sha512", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("sha512 layer"))
	root := addTestIndex(t, source, "sha256", []Descriptor{child})

	resolver := NewResolver(source)
	_, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrDigestAlgorithmMismatch) {
		t.Fatalf("Resolve() error = %v, want ErrDigestAlgorithmMismatch", err)
	}

	permissive := NewResolverWithOptions(source, nil, ResolverOptions{
		AllowMixedDigestAlgorithms: true,
	})
	if _, err := permissive.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	}); err != nil {
		t.Fatalf("permissive Resolve() error = %v", err)
	}
}

func TestAttestationsAreNotPlatformCandidates(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	image := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("real image"))
	attestation := image
	attestation.Platform = &Platform{
		OS:           "unknown",
		Architecture: "unknown",
	}
	attestation.Annotations = map[string]string{
		"vnd.docker.reference.type":         "attestation-manifest",
		"org.opencontainers.image.platform": "linux/amd64",
	}
	secondAttestation := attestation
	secondAttestation.Digest = digestOf(t, "sha256", []byte("second attestation")).String()
	root := addTestIndex(t, source, "sha256", []Descriptor{image, attestation, secondAttestation})

	resolver := NewResolver(source)
	resolved, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ManifestDigest.String() != image.Digest {
		t.Fatalf("selected %s, want image %s", resolved.ManifestDigest, image.Digest)
	}
}

func TestSubjectReferrerIsNotRunnable(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	image := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("referrer payload"))
	manifest, _ := loadTestManifestAndConfig(t, source, image)
	manifest.Subject = &Descriptor{
		MediaType: MediaTypeOCIManifest,
		Digest:    digestOf(t, "sha256", []byte("covered image")).String(),
		Size:      128,
	}
	manifestBytes := mustJSON(t, manifest)
	referrer := putTestBlob(t, source, "sha256", manifestBytes, MediaTypeOCIManifest)
	referrer.Platform = &Platform{OS: "linux", Architecture: "amd64"}
	root := addTestIndex(t, source, "sha256", []Descriptor{referrer})

	resolver := NewResolver(source)
	_, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrNotImageManifest) {
		t.Fatalf("Resolve() error = %v, want ErrNotImageManifest", err)
	}
}

func TestLegalSHA512Chain(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	child := addTestImage(t, source, "sha512", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("sha512 chain"))
	root := addTestIndex(t, source, "sha512", []Descriptor{child})

	resolver := NewResolver(source)
	image, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.Algorithm() != "sha512" {
		t.Fatalf("manifest algorithm = %s, want sha512", image.ManifestDigest.Algorithm())
	}
}

func TestNestedIndexSelection(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	amd64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("nested amd64"))
	arm64 := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm64",
	}, []byte("nested arm64"))
	nested := addTestIndex(t, source, "sha256", []Descriptor{amd64, arm64})
	nestedDescriptor := Descriptor{
		MediaType: MediaTypeOCIIndex,
		Digest:    nested.String(),
		Platform: &Platform{
			OS:           "linux",
			Architecture: "arm64",
		},
	}
	nestedBytes, err := source.FetchBlob(context.Background(), nested.String())
	if err != nil {
		t.Fatalf("FetchBlob(nested index) error = %v", err)
	}
	nestedDescriptor.Size = int64(len(nestedBytes))
	root := addTestIndex(t, source, "sha256", []Descriptor{nestedDescriptor})

	resolver := NewResolver(source)
	image, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "arm64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if image.ManifestDigest.String() != arm64.Digest {
		t.Fatalf("selected %s, want %s", image.ManifestDigest, arm64.Digest)
	}
}

func TestCacheIsDigestAddressed(t *testing.T) {
	t.Parallel()

	memory := NewMemorySource()
	child := addTestImage(t, memory, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("cache layer"))
	root := addTestIndex(t, memory, "sha256", []Descriptor{child})
	source := &countingBlobSource{inner: memory, fetches: make(map[string]int)}

	resolver := NewResolver(source)
	first, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	firstFetchCount := source.fetchCount()
	if firstFetchCount != 4 {
		t.Fatalf("first fetch count = %d, want index, manifest, config, layer", firstFetchCount)
	}

	second, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if source.fetchCount() != firstFetchCount {
		t.Fatalf("cache hit fetched more blobs: %d -> %d", firstFetchCount, source.fetchCount())
	}
	if second.ManifestDigest != first.ManifestDigest {
		t.Fatalf("cached manifest = %s, want %s", second.ManifestDigest, first.ManifestDigest)
	}
	if resolver.CachedBlobCount() != 4 {
		t.Fatalf("CachedBlobCount() = %d, want 4", resolver.CachedBlobCount())
	}
}

func TestConfigPlatformMismatchFails(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	child := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v7",
	}, []byte("arm layer"))
	index := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     []Descriptor{child},
	}
	root := putTestIndex(t, source, "sha256", index)

	manifest, _ := loadTestManifestAndConfig(t, source, child)
	configBytes, err := source.FetchBlob(context.Background(), manifest.Config.Digest)
	if err != nil {
		t.Fatalf("FetchBlob(config) error = %v", err)
	}
	var config ImageConfig
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("json.Unmarshal(config) error = %v", err)
	}
	config.Variant = "v6"
	tamperedConfig := mustJSON(t, config)
	configDigest := digestOf(t, "sha256", tamperedConfig)
	mustPutBlob(t, source, configDigest.String(), tamperedConfig)

	manifest.Config.Digest = configDigest.String()
	manifest.Config.Size = int64(len(tamperedConfig))
	manifestBytes := mustJSON(t, manifest)
	manifestDigest := digestOf(t, "sha256", manifestBytes)
	mustPutBlob(t, source, manifestDigest.String(), manifestBytes)
	child.Digest = manifestDigest.String()
	child.Size = int64(len(manifestBytes))
	root = putTestIndex(t, source, "sha256", Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     []Descriptor{child},
	})

	resolver := NewResolver(source)
	_, err = resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "arm",
		Variant:      "v7",
	})
	if !errors.Is(err, ErrConfigPlatformMismatch) {
		t.Fatalf("Resolve() error = %v, want ErrConfigPlatformMismatch", err)
	}
}

func TestLayerCountMismatchFails(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	config := ImageConfig{
		OS:           "linux",
		Architecture: "amd64",
		RootFS: &RootFSConfig{
			Type: "layers",
			DiffIDs: []string{
				digestOf(t, "sha256", []byte("one")).String(),
				digestOf(t, "sha256", []byte("two")).String(),
			},
		},
	}
	configBytes := mustJSON(t, config)
	configDescriptor := putTestBlob(t, source, "sha256", configBytes, MediaTypeOCIConfig)
	layerDescriptor := putTestBlob(t, source, "sha256", []byte("one layer"), MediaTypeOCILayer)
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		Config:        configDescriptor,
		Layers:        []Descriptor{layerDescriptor},
	}
	manifestBytes := mustJSON(t, manifest)
	manifestDescriptor := putTestBlob(t, source, "sha256", manifestBytes, MediaTypeOCIManifest)
	manifestDescriptor.Platform = &Platform{OS: "linux", Architecture: "amd64"}
	root := addTestIndex(t, source, "sha256", []Descriptor{manifestDescriptor})

	resolver := NewResolver(source)
	_, err := resolver.Resolve(context.Background(), root.String(), Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if !errors.Is(err, ErrLayerCountMismatch) || !errors.Is(err, ErrMissingLayer) {
		t.Fatalf("Resolve() error = %v, want ErrLayerCountMismatch and ErrMissingLayer", err)
	}
}

func TestSingleManifestResolution(t *testing.T) {
	t.Parallel()

	source := NewMemorySource()
	image := addTestImage(t, source, "sha256", Platform{
		OS:           "linux",
		Architecture: "amd64",
	}, []byte("single manifest"))

	resolver := NewResolver(source)
	resolved, err := resolver.Resolve(context.Background(), image.Digest, Platform{
		OS:           "linux",
		Architecture: "amd64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ManifestDigest.String() != image.Digest {
		t.Fatalf("ManifestDigest = %s, want %s", resolved.ManifestDigest, image.Digest)
	}
	if resolved.RootDigest != resolved.ManifestDigest {
		t.Fatalf("RootDigest = %s, want %s", resolved.RootDigest, resolved.ManifestDigest)
	}
}

type countingBlobSource struct {
	inner   *MemorySource
	mutex   sync.Mutex
	fetches map[string]int
}

func (source *countingBlobSource) FetchBlob(ctx context.Context, digest string) ([]byte, error) {
	source.mutex.Lock()
	source.fetches[digest]++
	source.mutex.Unlock()
	return source.inner.FetchBlob(ctx, digest)
}

func (source *countingBlobSource) fetchCount() int {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	total := 0
	for _, count := range source.fetches {
		total += count
	}
	return total
}

func addTestImage(
	t *testing.T,
	source *MemorySource,
	algorithm string,
	platform Platform,
	layerContent []byte,
) Descriptor {
	t.Helper()

	config := ImageConfig{
		OS:           platform.OS,
		Architecture: platform.Architecture,
		Variant:      platform.Variant,
		OSVersion:    platform.OSVersion,
		RootFS: &RootFSConfig{
			Type:    "layers",
			DiffIDs: []string{digestOf(t, "sha256", layerContent).String()},
		},
	}
	configBytes := mustJSON(t, config)
	configDescriptor := putTestBlob(t, source, algorithm, configBytes, MediaTypeOCIConfig)
	layerDescriptor := putTestBlob(t, source, algorithm, layerContent, MediaTypeOCILayer)

	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		Config:        configDescriptor,
		Layers:        []Descriptor{layerDescriptor},
	}
	manifestBytes := mustJSON(t, manifest)
	manifestDescriptor := putTestBlob(t, source, algorithm, manifestBytes, MediaTypeOCIManifest)
	manifestDescriptor.Platform = &platform
	return manifestDescriptor
}

func addTestIndex(
	t *testing.T,
	source *MemorySource,
	algorithm string,
	manifests []Descriptor,
) Digest {
	t.Helper()
	return putTestIndex(t, source, algorithm, Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     manifests,
	})
}

func putTestIndex(t *testing.T, source *MemorySource, algorithm string, index Index) Digest {
	t.Helper()
	return Digest(putTestBlob(t, source, algorithm, mustJSON(t, index), MediaTypeOCIIndex).Digest)
}

func putTestBlob(
	t *testing.T,
	source *MemorySource,
	algorithm string,
	content []byte,
	mediaType string,
) Descriptor {
	t.Helper()
	digest, err := source.PutBlobContent(algorithm, content)
	if err != nil {
		t.Fatalf("PutBlobContent() error = %v", err)
	}
	return Descriptor{
		MediaType: mediaType,
		Digest:    digest.String(),
		Size:      int64(len(content)),
	}
}

func mustPutBlob(t *testing.T, source *MemorySource, digest string, content []byte) {
	t.Helper()
	if err := source.PutBlob(digest, content); err != nil {
		t.Fatalf("PutBlob(%s) error = %v", digest, err)
	}
}

func mustTag(t *testing.T, source *MemorySource, reference string, digest Digest) {
	t.Helper()
	if err := source.PutTag(reference, digest.String()); err != nil {
		t.Fatalf("PutTag(%s) error = %v", reference, err)
	}
}

func digestOf(t *testing.T, algorithm string, content []byte) Digest {
	t.Helper()
	digest, err := ComputeDigest(algorithm, content)
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	return digest
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return content
}

func loadTestManifestAndConfig(
	t *testing.T,
	source *MemorySource,
	manifestDescriptor Descriptor,
) (Manifest, ImageConfig) {
	t.Helper()
	manifestBytes, err := source.FetchBlob(context.Background(), manifestDescriptor.Digest)
	if err != nil {
		t.Fatalf("FetchBlob(manifest) error = %v", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest) error = %v", err)
	}
	configBytes, err := source.FetchBlob(context.Background(), manifest.Config.Digest)
	if err != nil {
		t.Fatalf("FetchBlob(config) error = %v", err)
	}
	var config ImageConfig
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("json.Unmarshal(config) error = %v", err)
	}
	return manifest, config
}

func ExampleResolver() {
	source := NewMemorySource()
	_ = source
	fmt.Println("use NewMemorySource and NewResolver")
	// Output: use NewMemorySource and NewResolver
}
