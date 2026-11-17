package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

const (
	sigType  = "application/vnd.dev.cosign.artifact.sig.v1+json"
	attType  = "application/vnd.in-toto+json"
	evilType = "application/vnd.evil.payload"
)

// fakeRegistry serves manifest blobs by digest, like a minimal OCI registry.
type fakeRegistry struct {
	blobs map[string][]byte
}

func (r *fakeRegistry) fetch(_ context.Context, digest string) ([]byte, error) {
	blob, ok := r.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("not found: %s", digest)
	}
	return blob, nil
}

func digestOf(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// addManifest stores a manifest for subjectDigest and returns its descriptor.
func (r *fakeRegistry) addManifest(t *testing.T, subjectDigest, artifactType string, nonce string) Descriptor {
	t.Helper()
	if r.blobs == nil {
		r.blobs = map[string][]byte{}
	}
	blob, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"artifactType":  artifactType,
		"subject": map[string]any{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    subjectDigest,
			"size":      1234,
		},
		"layers":      []any{},
		"annotations": map[string]any{"test/nonce": nonce},
	})
	if err != nil {
		t.Fatal(err)
	}
	dgst := digestOf(blob)
	r.blobs[dgst] = blob
	return Descriptor{
		MediaType:    "application/vnd.oci.image.manifest.v1+json",
		Digest:       dgst,
		Size:         int64(len(blob)),
		ArtifactType: artifactType,
	}
}

var testSubject = digestOf([]byte("the image manifest being verified"))

func allowList() map[string]bool {
	return map[string]bool{sigType: true, attType: true}
}

func digests(ds []Descriptor) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Digest)
	}
	return out
}

// TestFilterReferrersPoisonedList feeds a poisoned referrers list through
// the filter and checks that only genuine referrers survive to signature
// verification.
func TestFilterReferrersPoisonedList(t *testing.T) {
	reg := &fakeRegistry{}

	goodSig := reg.addManifest(t, testSubject, sigType, "sig")
	goodAtt := reg.addManifest(t, testSubject, attType, "att")

	// Forged signature: manifest points at a different subject.
	fakeSubject := reg.addManifest(t, digestOf([]byte("some other image")), sigType, "fake")

	// Swapped content: descriptor digest names a blob whose bytes were
	// replaced after listing, so the content hash no longer matches.
	swapped := reg.addManifest(t, testSubject, sigType, "swapped")
	reg.blobs[swapped.Digest] = []byte(`{"schemaVersion":2,"subject":{"digest":"` + testSubject + `"}}`)

	// Disallowed artifact type.
	evil := reg.addManifest(t, testSubject, evilType, "evil")

	// Missing blob: registry lost the manifest.
	missing := reg.addManifest(t, testSubject, sigType, "missing")
	delete(reg.blobs, missing.Digest)

	listed := []Descriptor{
		goodSig,
		fakeSubject,
		swapped,
		evil,
		missing,
		goodAtt,
		goodSig, // duplicate digest must not be verified twice
	}

	kept := FilterReferrers(context.Background(), testSubject, listed, allowList(), reg.fetch)

	// Everything that survives goes to signature verification.
	verified := digests(kept)
	want := []string{goodSig.Digest, goodAtt.Digest}
	if fmt.Sprint(verified) != fmt.Sprint(want) {
		t.Fatalf("verified set = %v, want %v", verified, want)
	}
	for _, bad := range []Descriptor{fakeSubject, swapped, evil, missing} {
		for _, k := range kept {
			if k.Digest == bad.Digest {
				t.Errorf("poisoned descriptor %s reached signature verification", bad.Digest)
			}
		}
	}
}

// TestDuplicateDigestFetchedOnce ensures a repeated digest in the list is
// only fetched and admitted once.
func TestDuplicateDigestFetchedOnce(t *testing.T) {
	reg := &fakeRegistry{}
	good := reg.addManifest(t, testSubject, sigType, "sig")

	fetches := 0
	counting := func(ctx context.Context, digest string) ([]byte, error) {
		fetches++
		return reg.fetch(ctx, digest)
	}

	kept := FilterReferrers(context.Background(), testSubject,
		[]Descriptor{good, good, good}, allowList(), counting)
	if len(kept) != 1 {
		t.Fatalf("kept %d descriptors, want 1", len(kept))
	}
	if fetches != 1 {
		t.Fatalf("fetched %d times, want 1", fetches)
	}
}

// TestFallbackIndex verifies the sha256-<hex> fallback tag path applies the
// same filtering as the referrers API path.
func TestFallbackIndex(t *testing.T) {
	reg := &fakeRegistry{}
	good := reg.addManifest(t, testSubject, sigType, "sig")
	fakeSubject := reg.addManifest(t, digestOf([]byte("unrelated subject")), attType, "fake")
	evil := reg.addManifest(t, testSubject, evilType, "evil")

	tag, err := FallbackTag(testSubject)
	if err != nil {
		t.Fatal(err)
	}
	wantTag := "sha256-" + testSubject[len("sha256:"):]
	if tag != wantTag {
		t.Fatalf("FallbackTag = %q, want %q", tag, wantTag)
	}

	idxBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests":     []Descriptor{good, fakeSubject, evil, good},
	})
	if err != nil {
		t.Fatal(err)
	}

	descs, err := ParseFallbackIndex(idxBytes)
	if err != nil {
		t.Fatal(err)
	}
	kept := FilterReferrers(context.Background(), testSubject, descs, allowList(), reg.fetch)
	if len(kept) != 1 || kept[0].Digest != good.Digest {
		t.Fatalf("fallback path kept %v, want only %s", digests(kept), good.Digest)
	}
}

// TestFallbackTagRejectsNonSHA256 ensures non-sha256 subjects have no
// fallback tag.
func TestFallbackTagRejectsNonSHA256(t *testing.T) {
	for _, s := range []string{"", "sha512:abcd", "sha256:xyz", "sha256:abcd"} {
		if _, err := FallbackTag(s); !errors.Is(err, ErrNoSubject) {
			t.Errorf("FallbackTag(%q) err = %v, want ErrNoSubject", s, err)
		}
	}
}

// TestNilFetcher ensures a nil fetcher admits nothing rather than panicking.
func TestNilFetcher(t *testing.T) {
	reg := &fakeRegistry{}
	good := reg.addManifest(t, testSubject, sigType, "sig")
	if kept := FilterReferrers(context.Background(), testSubject, []Descriptor{good}, allowList(), nil); len(kept) != 0 {
		t.Fatalf("nil fetcher kept %d descriptors, want 0", len(kept))
	}
}
