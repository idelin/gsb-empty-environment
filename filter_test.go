package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	signatureType   = "application/vnd.example.signature.v1+json"
	attestationType = "application/vnd.example.attestation.v1+json"
	malwareType     = "application/vnd.example.evil.v1+json"
)

type fakeFetcher struct {
	manifests map[string][]byte
	errors    map[string]error
	calls     []string
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		manifests: make(map[string][]byte),
		errors:    make(map[string]error),
	}
}

func (f *fakeFetcher) FetchManifest(_ context.Context, reference string) ([]byte, error) {
	f.calls = append(f.calls, reference)
	if err, ok := f.errors[reference]; ok {
		return nil, err
	}
	content, ok := f.manifests[reference]
	if !ok {
		return nil, errors.New("manifest not found: " + reference)
	}
	return content, nil
}

func (f *fakeFetcher) called(reference string) bool {
	for _, call := range f.calls {
		if call == reference {
			return true
		}
	}
	return false
}

type recordingVerifier struct {
	digests []string
	err     error
}

func (v *recordingVerifier) Verify(_ context.Context, descriptor Descriptor, _ []byte) error {
	v.digests = append(v.digests, descriptor.Digest)
	return v.err
}

func shaDigest(t *testing.T, content string) string {
	t.Helper()
	digest, err := ComputeDigest("sha256", []byte(content))
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	return digest
}

func makeManifest(t *testing.T, subject, artifactType string) []byte {
	t.Helper()
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		ArtifactType:  artifactType,
		Subject: &Descriptor{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    subject,
			Size:      1,
		},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return content
}

func makeDescriptor(t *testing.T, content []byte, artifactType string) Descriptor {
	t.Helper()
	digest, err := ComputeDigest("sha256", content)
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	return Descriptor{
		MediaType:    "application/vnd.oci.image.manifest.v1+json",
		Digest:       digest,
		Size:         int64(len(content)),
		ArtifactType: artifactType,
	}
}

func makeIndex(t *testing.T, descriptors ...Descriptor) []byte {
	t.Helper()
	index := Index{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests:     descriptors,
	}
	content, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return content
}

func TestFilterReferrersAcceptsMatchingManifest(t *testing.T) {
	subject := shaDigest(t, "subject image")
	content := makeManifest(t, subject, signatureType)
	descriptor := makeDescriptor(t, content, signatureType)

	fetcher := newFakeFetcher()
	fetcher.manifests[descriptor.Digest] = content
	verifier := &recordingVerifier{}

	result, err := FilterAndVerify(
		context.Background(),
		subject,
		[]Descriptor{descriptor},
		NewAllowlist(signatureType),
		fetcher,
		verifier.Verify,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("len(Accepted) = %d, want 1; rejected = %+v", len(result.Accepted), result.Rejected)
	}
	if result.Accepted[0].Digest != descriptor.Digest {
		t.Errorf("accepted digest = %q, want %q", result.Accepted[0].Digest, descriptor.Digest)
	}
	if len(result.Rejected) != 0 {
		t.Errorf("len(Rejected) = %d, want 0", len(result.Rejected))
	}
	if len(verifier.digests) != 1 || verifier.digests[0] != descriptor.Digest {
		t.Errorf("verified digests = %v, want [%s]", verifier.digests, descriptor.Digest)
	}
}

func TestFilterReferrersRejectsAdversarialEntriesBeforeVerification(t *testing.T) {
	subject := shaDigest(t, "subject image")
	otherSubject := shaDigest(t, "attacker selected subject")

	validContent := makeManifest(t, subject, signatureType)
	validDescriptor := makeDescriptor(t, validContent, signatureType)

	fakeSubjectContent := makeManifest(t, otherSubject, signatureType)
	fakeSubjectDescriptor := makeDescriptor(t, fakeSubjectContent, signatureType)

	swappedContent := makeManifest(t, subject, attestationType)
	swappedDescriptor := makeDescriptor(t, validContent, attestationType)
	_ = swappedContent

	disallowedContent := makeManifest(t, subject, malwareType)
	disallowedDescriptor := makeDescriptor(t, disallowedContent, malwareType)

	spoofedTypeContent := makeManifest(t, subject, malwareType)
	spoofedTypeDescriptor := makeDescriptor(t, spoofedTypeContent, signatureType)

	tests := []struct {
		name        string
		descriptors []Descriptor
		fetch       map[string][]byte
		wantFetch   []string
		wantAccept  int
	}{
		{
			name:        "valid baseline",
			descriptors: []Descriptor{validDescriptor},
			fetch:       map[string][]byte{validDescriptor.Digest: validContent},
			wantFetch:   []string{validDescriptor.Digest},
			wantAccept:  1,
		},
		{
			name:        "fake subject",
			descriptors: []Descriptor{fakeSubjectDescriptor},
			fetch:       map[string][]byte{fakeSubjectDescriptor.Digest: fakeSubjectContent},
			wantFetch:   []string{fakeSubjectDescriptor.Digest},
			wantAccept:  0,
		},
		{
			name:        "swapped content",
			descriptors: []Descriptor{swappedDescriptor},
			fetch:       map[string][]byte{swappedDescriptor.Digest: swappedContent},
			wantFetch:   []string{swappedDescriptor.Digest},
			wantAccept:  0,
		},
		{
			name:        "artifact type not allowlisted",
			descriptors: []Descriptor{disallowedDescriptor},
			fetch:       map[string][]byte{disallowedDescriptor.Digest: disallowedContent},
			wantFetch:   nil,
			wantAccept:  0,
		},
		{
			name:        "manifest type spoofed by descriptor",
			descriptors: []Descriptor{spoofedTypeDescriptor},
			fetch:       map[string][]byte{spoofedTypeDescriptor.Digest: spoofedTypeContent},
			wantFetch:   []string{spoofedTypeDescriptor.Digest},
			wantAccept:  0,
		},
		{
			name:        "invalid descriptor digest",
			descriptors: []Descriptor{{Digest: "not-a-digest", ArtifactType: signatureType}},
			fetch:       nil,
			wantFetch:   nil,
			wantAccept:  0,
		},
		{
			name:        "manifest without subject",
			descriptors: []Descriptor{{Digest: shaDigest(t, "no subject"), Size: int64(len("{}")), ArtifactType: signatureType}},
			fetch:       map[string][]byte{shaDigest(t, "no subject"): []byte("{}")},
			wantFetch:   []string{shaDigest(t, "no subject")},
			wantAccept:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := newFakeFetcher()
			for digest, content := range test.fetch {
				fetcher.manifests[digest] = content
			}
			verifier := &recordingVerifier{}

			result, err := FilterAndVerify(
				context.Background(),
				subject,
				test.descriptors,
				NewAllowlist(signatureType, attestationType),
				fetcher,
				verifier.Verify,
			)
			if err != nil {
				t.Fatalf("FilterAndVerify() error = %v", err)
			}
			if len(result.Accepted) != test.wantAccept {
				t.Fatalf("len(Accepted) = %d, want %d; rejected = %+v", len(result.Accepted), test.wantAccept, result.Rejected)
			}
			if len(verifier.digests) != test.wantAccept {
				t.Fatalf("verifier saw %d entries, want %d: %v", len(verifier.digests), test.wantAccept, verifier.digests)
			}
			for _, digest := range test.wantFetch {
				if !fetcher.called(digest) {
					t.Errorf("fetcher did not receive %q; calls = %v", digest, fetcher.calls)
				}
			}
			if len(test.wantFetch) == 0 && len(fetcher.calls) != 0 {
				t.Errorf("fetcher calls = %v, want none", fetcher.calls)
			}
			for _, verified := range verifier.digests {
				if !containsDigest(result.Accepted, verified) {
					t.Errorf("verifier saw filtered digest %q", verified)
				}
			}
		})
	}
}

func TestFilterReferrersDeduplicatesDigests(t *testing.T) {
	subject := shaDigest(t, "subject image")
	content := makeManifest(t, subject, signatureType)
	descriptor := makeDescriptor(t, content, signatureType)

	fetcher := newFakeFetcher()
	fetcher.manifests[descriptor.Digest] = content
	verifier := &recordingVerifier{}

	result, err := FilterAndVerify(
		context.Background(),
		subject,
		[]Descriptor{descriptor, descriptor, descriptor},
		NewAllowlist(signatureType),
		fetcher,
		verifier.Verify,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("len(Accepted) = %d, want 1", len(result.Accepted))
	}
	if len(verifier.digests) != 1 {
		t.Fatalf("verifier calls = %d, want 1", len(verifier.digests))
	}
	if fetcher.calls[0] != descriptor.Digest || len(fetcher.calls) != 1 {
		t.Fatalf("fetch calls = %v, want one call for %s", fetcher.calls, descriptor.Digest)
	}
}

func TestFilterFallbackIndexAppliesSameChecks(t *testing.T) {
	subject := shaDigest(t, "subject image")
	otherSubject := shaDigest(t, "attacker selected subject")

	validContent := makeManifest(t, subject, signatureType)
	validDescriptor := makeDescriptor(t, validContent, "")
	duplicateDescriptor := makeDescriptor(t, validContent, signatureType)

	fakeContent := makeManifest(t, otherSubject, signatureType)
	fakeDescriptor := makeDescriptor(t, fakeContent, signatureType)

	swappedSourceContent := makeManifest(t, subject, attestationType)
	swappedDescriptor := makeDescriptor(t, swappedSourceContent, signatureType)
	swappedContent := makeManifest(t, subject, signatureType)

	disallowedContent := makeManifest(t, subject, malwareType)
	disallowedDescriptor := makeDescriptor(t, disallowedContent, malwareType)

	index := makeIndex(t, validDescriptor, duplicateDescriptor, fakeDescriptor, swappedDescriptor, disallowedDescriptor)
	tag, err := FallbackTag(subject)
	if err != nil {
		t.Fatalf("FallbackTag() error = %v", err)
	}
	if tag != strings.ReplaceAll(subject, ":", "-") {
		t.Fatalf("FallbackTag() = %q", tag)
	}

	fetcher := newFakeFetcher()
	fetcher.manifests[tag] = index
	fetcher.manifests[validDescriptor.Digest] = validContent
	fetcher.manifests[fakeDescriptor.Digest] = fakeContent
	fetcher.manifests[swappedDescriptor.Digest] = swappedContent
	fetcher.manifests[disallowedDescriptor.Digest] = disallowedContent

	verifier := &recordingVerifier{}
	result, err := FilterFallbackAndVerify(
		context.Background(),
		subject,
		NewAllowlist(signatureType),
		fetcher,
		verifier.Verify,
	)
	if err != nil {
		t.Fatalf("FilterFallbackAndVerify() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("len(Accepted) = %d, want 1; rejected = %+v", len(result.Accepted), result.Rejected)
	}
	if result.Accepted[0].ArtifactType != signatureType {
		t.Errorf("accepted artifactType = %q, want %q", result.Accepted[0].ArtifactType, signatureType)
	}
	if len(verifier.digests) != 1 || verifier.digests[0] != validDescriptor.Digest {
		t.Errorf("verified digests = %v, want [%s]", verifier.digests, validDescriptor.Digest)
	}
	if !fetcher.called(tag) {
		t.Errorf("fallback tag %q was not fetched; calls = %v", tag, fetcher.calls)
	}
	if fetcher.called(disallowedDescriptor.Digest) {
		t.Errorf("disallowed descriptor was fetched: %v", fetcher.calls)
	}
	if !fetcher.called(swappedDescriptor.Digest) {
		t.Errorf("swapped-content descriptor was not checked: %v", fetcher.calls)
	}
}

func TestFilterReferrersWithFallbackUsesTagOnlyWhenListIsEmpty(t *testing.T) {
	subject := shaDigest(t, "subject image")
	content := makeManifest(t, subject, signatureType)
	descriptor := makeDescriptor(t, content, signatureType)

	fetcher := newFakeFetcher()
	fetcher.manifests[descriptor.Digest] = content
	index := makeIndex(t, descriptor)
	tag, err := FallbackTag(subject)
	if err != nil {
		t.Fatalf("FallbackTag() error = %v", err)
	}
	fetcher.manifests[tag] = index

	fromReferrers, err := FilterReferrersWithFallback(
		context.Background(),
		subject,
		[]Descriptor{descriptor},
		NewAllowlist(signatureType),
		fetcher,
	)
	if err != nil {
		t.Fatalf("FilterReferrersWithFallback(referrers) error = %v", err)
	}
	if len(fromReferrers) != 1 {
		t.Fatalf("referrers accepted = %d, want 1", len(fromReferrers))
	}
	if fetcher.called(tag) {
		t.Fatal("fallback tag fetched even though referrers list was non-empty")
	}

	fromFallback, err := FilterReferrersWithFallback(
		context.Background(),
		subject,
		nil,
		NewAllowlist(signatureType),
		fetcher,
	)
	if err != nil {
		t.Fatalf("FilterReferrersWithFallback(fallback) error = %v", err)
	}
	if len(fromFallback) != 1 {
		t.Fatalf("fallback accepted = %d, want 1", len(fromFallback))
	}
	if !fetcher.called(tag) {
		t.Fatal("fallback tag was not fetched")
	}
}

func TestFilterSourcesDeduplicatesAcrossReferrersAndFallback(t *testing.T) {
	subject := shaDigest(t, "subject image")
	content := makeManifest(t, subject, signatureType)
	descriptor := makeDescriptor(t, content, signatureType)

	fetcher := newFakeFetcher()
	fetcher.manifests[descriptor.Digest] = content

	result, err := FilterSources(
		context.Background(),
		subject,
		[]Descriptor{descriptor},
		makeIndex(t, descriptor),
		NewAllowlist(signatureType),
		fetcher,
	)
	if err != nil {
		t.Fatalf("FilterSources() error = %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("len(Accepted) = %d, want 1", len(result.Accepted))
	}
	if len(fetcher.calls) != 1 {
		t.Fatalf("fetch calls = %v, want one", fetcher.calls)
	}
}

func TestFilterRejectsVerifierFailure(t *testing.T) {
	subject := shaDigest(t, "subject image")
	content := makeManifest(t, subject, signatureType)
	descriptor := makeDescriptor(t, content, signatureType)

	fetcher := newFakeFetcher()
	fetcher.manifests[descriptor.Digest] = content

	result, err := FilterAndVerify(
		context.Background(),
		subject,
		[]Descriptor{descriptor},
		NewAllowlist(signatureType),
		fetcher,
		func(context.Context, Descriptor, []byte) error {
			return errors.New("bad signature")
		},
	)
	if err != nil {
		t.Fatalf("FilterAndVerify() error = %v", err)
	}
	if len(result.Accepted) != 0 {
		t.Fatalf("len(Accepted) = %d, want 0", len(result.Accepted))
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != "verification failed" {
		t.Fatalf("rejected = %+v, want verification failure", result.Rejected)
	}
}

func TestFallbackTagRejectsInvalidDigest(t *testing.T) {
	if _, err := FallbackTag("sha256:not-hex"); err == nil {
		t.Fatal("FallbackTag() accepted invalid digest")
	}
}

func containsDigest(descriptors []Descriptor, digest string) bool {
	for _, descriptor := range descriptors {
		if descriptor.Digest == digest {
			return true
		}
	}
	return false
}
