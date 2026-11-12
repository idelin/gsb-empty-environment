package referrerfilter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	testArtifactSignature   = "application/vnd.example.signature.v1+json"
	testArtifactAttestation = "application/vnd.example.attestation.v1+json"
	testArtifactUnknown     = "application/vnd.example.malicious.v1+json"
)

// fakeRegistry is an in-memory Registry. Each referrer list is keyed by the
// queried subject digest; manifests are keyed by their digest string.
type fakeRegistry struct {
	referrers      map[string][]Descriptor
	referrersError map[string]error
	manifests      map[string][]byte
	tags           map[string][]byte
	fetched        []string
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		referrers:      map[string][]Descriptor{},
		referrersError: map[string]error{},
		manifests:      map[string][]byte{},
		tags:           map[string][]byte{},
	}
}

func (f *fakeRegistry) Referrers(_ context.Context, _ string, subjectDigest Digest) ([]Descriptor, error) {
	if err := f.referrersError[subjectDigest.String()]; err != nil {
		return nil, err
	}
	return f.referrers[subjectDigest.String()], nil
}

func (f *fakeRegistry) FetchManifest(_ context.Context, _ string, descriptorDigest Digest) ([]byte, error) {
	f.fetched = append(f.fetched, "digest:"+descriptorDigest.String())
	raw, ok := f.manifests[descriptorDigest.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrManifestNotFound, descriptorDigest)
	}
	return append([]byte(nil), raw...), nil
}

func (f *fakeRegistry) FetchManifestByTag(_ context.Context, _, tag string) ([]byte, error) {
	f.fetched = append(f.fetched, "tag:"+tag)
	raw, ok := f.tags[tag]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrManifestNotFound, tag)
	}
	return append([]byte(nil), raw...), nil
}

// recordingVerifier records every descriptor it is asked to verify. Its
// presence in the call list proves a descriptor crossed every trust gate.
type recordingVerifier struct {
	calls   []string
	failOn  map[string]bool
	failErr error
}

func newRecordingVerifier() *recordingVerifier {
	return &recordingVerifier{failOn: map[string]bool{}}
}

func (v *recordingVerifier) Verify(_ context.Context, descriptor Descriptor, _ []byte) error {
	v.calls = append(v.calls, descriptor.Digest.String())
	if v.failOn[descriptor.Digest.String()] {
		if v.failErr != nil {
			return v.failErr
		}
		return errors.New("verifier rejected artifact")
	}
	return nil
}

// buildManifest creates an image manifest bound to subject and returns its raw
// bytes together with a correctly sized, correctly digested descriptor.
func buildManifest(t *testing.T, subject Digest, artifactType string, mutate func(map[string]any)) ([]byte, Descriptor) {
	t.Helper()
	document := map[string]any{
		"schemaVersion": 2,
		"mediaType":     MediaTypeOCIManifest,
		"artifactType":  artifactType,
		"config": map[string]any{
			"mediaType": "application/vnd.oci.empty.v1+json",
			"digest":    "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
			"size":      2,
		},
		"layers": []any{},
		"subject": map[string]any{
			"mediaType": MediaTypeOCIManifest,
			"digest":    subject.String(),
			"size":      1234,
		},
	}
	if mutate != nil {
		mutate(document)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	digest, err := digestBytes(algSHA256, raw)
	if err != nil {
		t.Fatalf("digest manifest: %v", err)
	}
	return raw, Descriptor{
		MediaType:    MediaTypeOCIManifest,
		Digest:       digest,
		Size:         int64(len(raw)),
		ArtifactType: artifactType,
	}
}

// tamper flips a byte in a manifest without touching the descriptor, so the
// recomputed content digest can no longer match the pinned descriptor digest.
func tamper(raw []byte) []byte {
	modified := append([]byte(nil), raw...)
	last := modified[len(modified)-1]
	if last == '0' {
		modified[len(modified)-1] = '1'
	} else {
		modified[len(modified)-1] = '0'
	}
	return modified
}

func indexBytes(t *testing.T, descriptors []Descriptor) []byte {
	t.Helper()
	raw, err := json.Marshal(indexManifest{
		MediaType: MediaTypeOCIIndex,
		Manifests: descriptors,
	})
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	return raw
}

func rejectionReasons(result *Result) []string {
	found := make([]string, 0, len(result.Rejected))
	for _, rejection := range result.Rejected {
		found = append(found, rejection.Reason)
	}
	return found
}

func sortedStrings(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}

func mustDigest(t *testing.T, hexValue string) Digest {
	t.Helper()
	digest, err := ParseDigest("sha256:" + hexValue)
	if err != nil {
		t.Fatalf("parse digest: %v", err)
	}
	return digest
}

func TestFilterAndVerifyAcceptsValidReferrers(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000001")
	registry := newFakeRegistry()

	signatureRaw, signatureDescriptor := buildManifest(t, subject, testArtifactSignature, nil)
	attestationRaw, attestationDescriptor := buildManifest(t, subject, testArtifactAttestation, nil)
	registry.referrers[subject.String()] = []Descriptor{signatureDescriptor, attestationDescriptor}
	registry.manifests[signatureDescriptor.Digest.String()] = signatureRaw
	registry.manifests[attestationDescriptor.Digest.String()] = attestationRaw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature, testArtifactAttestation), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if result.FallbackUsed {
		t.Fatal("Referrers API path must not report fallback usage")
	}
	if len(result.Verified) != 2 {
		t.Fatalf("expected 2 verified references, got %d (rejections: %v)", len(result.Verified), rejectionReasons(result))
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("expected no rejections, got %v", rejectionReasons(result))
	}
	expectedCalls := []string{signatureDescriptor.Digest.String(), attestationDescriptor.Digest.String()}
	if !reflect.DeepEqual(verifier.calls, expectedCalls) {
		t.Fatalf("verifier calls = %v, want %v", verifier.calls, expectedCalls)
	}
}

func TestFilterAndVerifyRejectsArtifactTypeOutsideAllowList(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000002")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactUnknown, nil)
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[descriptor.Digest.String()] = raw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if len(result.Verified) != 0 {
		t.Fatalf("disallowed artifact must not be verified, got %d verified", len(result.Verified))
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonArtifactTypeNotAllowed}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("verifier must never see disallowed descriptor, saw %v", verifier.calls)
	}
	for _, fetched := range registry.fetched {
		if strings.HasPrefix(fetched, "digest:") {
			t.Fatalf("disallowed descriptor must not be fetched, but fetched %q", fetched)
		}
	}
}

func TestFilterAndVerifyRejectsForgedSubject(t *testing.T) {
	queriedSubject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000003")
	forgedSubject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000004")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, forgedSubject, testArtifactSignature, nil)
	registry.referrers[queriedSubject.String()] = []Descriptor{descriptor}
	registry.manifests[descriptor.Digest.String()] = raw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", queriedSubject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonSubjectMismatch}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("forged-subject artifact must not reach verifier, saw %v", verifier.calls)
	}
}

func TestFilterAndVerifyRejectsMissingSubject(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000005")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, func(document map[string]any) {
		delete(document, "subject")
	})
	digest, err := digestBytes(algSHA256, raw)
	if err != nil {
		t.Fatalf("digest manifest: %v", err)
	}
	descriptor.Digest = digest
	descriptor.Size = int64(len(raw))
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[digest.String()] = raw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonSubjectMissing}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("subject-less artifact must not reach verifier, saw %v", verifier.calls)
	}
}

func TestFilterAndVerifyRejectsSwappedContent(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000006")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	// The registry stores different bytes behind the same digest.
	registry.manifests[descriptor.Digest.String()] = tamper(raw)

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonDigestMismatch}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("tampered content must not reach verifier, saw %v", verifier.calls)
	}
}

func TestFilterAndVerifyRejectsSizeMismatch(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000007")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	descriptor.Size = int64(len(raw)) + 99
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[descriptor.Digest.String()] = raw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonSizeMismatch}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("size-mismatched artifact must not reach verifier, saw %v", verifier.calls)
	}
}

func TestFilterAndVerifyDeduplicatesRepeatedDigest(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000008")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	repeat := descriptor
	repeat.Annotations = map[string]string{"pushed-by": "attacker"}
	registry.referrers[subject.String()] = []Descriptor{descriptor, repeat}
	registry.manifests[descriptor.Digest.String()] = raw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if len(result.Verified) != 1 {
		t.Fatalf("expected one verified reference, got %d", len(result.Verified))
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonDuplicateDigest}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 1 || verifier.calls[0] != descriptor.Digest.String() {
		t.Fatalf("verifier should run once for the digest, got %v", verifier.calls)
	}
	var digestFetches int
	for _, fetched := range registry.fetched {
		if fetched == "digest:"+descriptor.Digest.String() {
			digestFetches++
		}
	}
	if digestFetches != 1 {
		t.Fatalf("manifest fetched %d times, want exactly 1", digestFetches)
	}
}

func TestFilterAndVerifyFallsBackToTagIndex(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000009")
	registry := newFakeRegistry()
	registry.referrersError[subject.String()] = ErrReferrersUnsupported

	goodRaw, goodDescriptor := buildManifest(t, subject, testArtifactSignature, nil)
	forgedRaw, forgedDescriptor := buildManifest(t,
		mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000010"),
		testArtifactSignature, nil)
	unknownRaw, unknownDescriptor := buildManifest(t, subject, testArtifactUnknown, nil)

	expectedTag := "sha256-" + subject.Hex()
	registry.tags[expectedTag] = indexBytes(t, []Descriptor{goodDescriptor, forgedDescriptor, unknownDescriptor})
	registry.manifests[goodDescriptor.Digest.String()] = goodRaw
	registry.manifests[forgedDescriptor.Digest.String()] = forgedRaw
	registry.manifests[unknownDescriptor.Digest.String()] = unknownRaw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if !result.FallbackUsed {
		t.Fatal("expected fallback tag path to be reported")
	}
	if len(result.Verified) != 1 || result.Verified[0].Descriptor.Digest.String() != goodDescriptor.Digest.String() {
		t.Fatalf("verified = %v, want only the correctly bound descriptor", result.Verified)
	}
	got := sortedStrings(rejectionReasons(result))
	want := sortedStrings([]string{ReasonSubjectMismatch, ReasonArtifactTypeNotAllowed})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rejection reasons = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(verifier.calls, []string{goodDescriptor.Digest.String()}) {
		t.Fatalf("verifier calls = %v", verifier.calls)
	}
	var sawTag bool
	for _, fetched := range registry.fetched {
		if fetched == "tag:"+expectedTag {
			sawTag = true
		}
	}
	if !sawTag {
		t.Fatalf("fallback tag %q was never fetched; fetches=%v", expectedTag, registry.fetched)
	}
}

func TestFallbackRejectsNonIndexTagContent(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000011")
	registry := newFakeRegistry()
	registry.referrersError[subject.String()] = ErrReferrersUnsupported

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	// A plain image manifest where the fallback index is expected.
	registry.tags["sha256-"+subject.Hex()] = raw
	registry.manifests[descriptor.Digest.String()] = raw

	_, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err == nil || !strings.Contains(err.Error(), "not an image index") {
		t.Fatalf("expected non-index fallback error, got %v", err)
	}
}

// wrappedRegistry confirms errors.Is is used to detect the fallback signal,
// so adapters that wrap the sentinel still trigger the fallback tag path.
type wrappedRegistry struct {
	*fakeRegistry
}

func (w wrappedRegistry) Referrers(ctx context.Context, repository string, subjectDigest Digest) ([]Descriptor, error) {
	if _, err := w.fakeRegistry.Referrers(ctx, repository, subjectDigest); err != nil {
		return nil, fmt.Errorf("adapter: %w", err)
	}
	return nil, nil
}

func TestFilterAndVerifyFallbackDetectsWrappedSentinel(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000017")
	inner := newFakeRegistry()
	inner.referrersError[subject.String()] = ErrReferrersUnsupported
	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	tag := "sha256-" + subject.Hex()
	inner.tags[tag] = indexBytes(t, []Descriptor{descriptor})
	inner.manifests[descriptor.Digest.String()] = raw

	result, err := FilterAndVerify(
		context.Background(), wrappedRegistry{fakeRegistry: inner}, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if !result.FallbackUsed {
		t.Fatal("wrapped ErrReferrersUnsupported must still trigger fallback")
	}
	if len(result.Verified) != 1 {
		t.Fatalf("expected one verified reference, got %d (%v)", len(result.Verified), rejectionReasons(result))
	}
}

func TestFilterAndVerifyHandlesMixedPoisonedList(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000012")
	registry := newFakeRegistry()

	goodRaw, good := buildManifest(t, subject, testArtifactSignature, nil)
	forgedSubjectRaw, forgedSubject := buildManifest(t,
		mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000013"),
		testArtifactSignature, nil)
	swappedRaw, swapped := buildManifest(t, subject, testArtifactAttestation, nil)
	unknownRaw, unknown := buildManifest(t, subject, testArtifactUnknown, nil)

	registry.referrers[subject.String()] = []Descriptor{unknown, forgedSubject, swapped, good, good}
	registry.manifests[good.Digest.String()] = goodRaw
	registry.manifests[forgedSubject.Digest.String()] = forgedSubjectRaw
	registry.manifests[swapped.Digest.String()] = tamper(swappedRaw)
	registry.manifests[unknown.Digest.String()] = unknownRaw

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature, testArtifactAttestation), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if len(result.Verified) != 1 || result.Verified[0].Descriptor.Digest.String() != good.Digest.String() {
		t.Fatalf("verified = %v, want only the single valid reference", result.Verified)
	}
	got := sortedStrings(rejectionReasons(result))
	want := sortedStrings([]string{
		ReasonArtifactTypeNotAllowed,
		ReasonSubjectMismatch,
		ReasonDigestMismatch,
		ReasonDuplicateDigest,
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rejection reasons = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(verifier.calls, []string{good.Digest.String()}) {
		t.Fatalf("only the valid descriptor may be verified, got %v", verifier.calls)
	}
}

func TestFilterAndVerifyReportsVerifierFailure(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000014")
	registry := newFakeRegistry()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[descriptor.Digest.String()] = raw

	verifier := newRecordingVerifier()
	verifier.failOn[descriptor.Digest.String()] = true
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if len(result.Verified) != 0 {
		t.Fatalf("verifier failure must not produce a verified reference, got %d", len(result.Verified))
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonVerifyFailed}) {
		t.Fatalf("rejection reasons = %v", got)
	}
}

func TestFilterAndVerifyRejectsMalformedSubjectAndDescriptorDigests(t *testing.T) {
	if _, err := FilterAndVerify(
		context.Background(), newFakeRegistry(), "example/app", "not-a-digest",
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	); err == nil {
		t.Fatal("expected error for malformed subject digest")
	}

	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000015")
	registry := newFakeRegistry()
	registry.referrers[subject.String()] = []Descriptor{{
		MediaType:    MediaTypeOCIManifest,
		ArtifactType: testArtifactSignature,
		Size:         10,
	}}

	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonBadDigest}) {
		t.Fatalf("rejection reasons = %v", got)
	}
}

func TestAllowListEmptyRejectsEverything(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000016")
	registry := newFakeRegistry()
	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[descriptor.Digest.String()] = raw

	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		AllowList{}, newRecordingVerifier(),
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if len(result.Verified) != 0 || len(result.Rejected) != 1 {
		t.Fatalf("zero-value allow list must reject all, got verified=%d rejected=%d", len(result.Verified), len(result.Rejected))
	}
}

func TestFilterAndVerifyRejectsMalformedManifestBytes(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000018")
	registry := newFakeRegistry()

	_, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	garbage := []byte("{not-json")
	garbageDigest, err := digestBytes(algSHA256, garbage)
	if err != nil {
		t.Fatalf("digest garbage: %v", err)
	}
	descriptor.Digest = garbageDigest
	descriptor.Size = int64(len(garbage))
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	registry.manifests[garbageDigest.String()] = garbage

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonMalformedManifest}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("malformed manifest must not reach verifier, saw %v", verifier.calls)
	}
}

func TestFilterAndVerifyRejectsFetchFailure(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000019")
	registry := newFakeRegistry()

	_, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	registry.referrers[subject.String()] = []Descriptor{descriptor}
	// Manifest deliberately never stored: FetchManifest returns not found.

	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonFetchFailed}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if len(verifier.calls) != 0 {
		t.Fatalf("unfetchable manifest must not reach verifier, saw %v", verifier.calls)
	}
}
