package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testSource(job string) Source {
	return Source{
		Workspace: "workspace-a",
		Scope:     "release",
		RunID:     "run-1",
		JobID:     job,
		Commit:    "0123456789abcdef",
	}
}

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestLegalHandoffPreservesOriginAndChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	payload := []byte("deterministic release artifact")
	builder := testSource("build")
	publisher := testSource("publish")

	artifact, err := store.UploadBytes(ctx, builder, "release/app.tgz", payload)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if artifact.Digest != ComputeDigest(payload) {
		t.Fatalf("digest = %s, want %s", artifact.Digest, ComputeDigest(payload))
	}
	if artifact.Source != builder {
		t.Fatalf("source binding = %+v, want %+v", artifact.Source, builder)
	}
	if artifact.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", artifact.Size, len(payload))
	}

	got, gotArtifact, err := store.DownloadBytes(ctx, publisher, "release/app.tgz")
	if err != nil {
		t.Fatalf("DownloadBytes: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %q, want %q", got, payload)
	}
	if gotArtifact.Source != builder {
		t.Fatalf("downloaded source = %+v, want original builder %+v", gotArtifact.Source, builder)
	}

	provenance, err := store.Provenance(ctx, publisher, "release/app.tgz")
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if len(provenance.Events) != 2 {
		t.Fatalf("events = %d, want upload and download", len(provenance.Events))
	}
	if provenance.Events[0].Kind != EventUpload || provenance.Events[0].Actor != builder {
		t.Fatalf("first event = %+v, want builder upload", provenance.Events[0])
	}
	if provenance.Events[1].Kind != EventDownload || provenance.Events[1].Actor != publisher {
		t.Fatalf("second event = %+v, want publisher download", provenance.Events[1])
	}
}

func TestSameNameDifferentDigestIsConflictAndOriginalWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	original := []byte("original trusted build")
	tampered := []byte("attacker supplied replacement")
	builder := testSource("build")
	attacker := testSource("untrusted-test")
	publisher := testSource("publish")
	const name = "release/app.tgz"

	if _, err := store.UploadBytes(ctx, builder, name, original); err != nil {
		t.Fatalf("original Upload: %v", err)
	}

	if _, err := store.UploadBytes(ctx, attacker, name, tampered); !errors.Is(err, ErrConflict) {
		t.Fatalf("attacker Upload error = %v, want ErrConflict", err)
	}

	got, artifact, err := store.DownloadBytes(ctx, publisher, name)
	if err != nil {
		t.Fatalf("publisher DownloadBytes: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("publisher received %q, want original %q", got, original)
	}
	if artifact.Digest != ComputeDigest(original) {
		t.Fatalf("publisher digest = %s, want %s", artifact.Digest, ComputeDigest(original))
	}

	provenance, err := store.Provenance(ctx, publisher, name)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	var sawConflict bool
	for _, event := range provenance.Events {
		if event.Kind == EventConflict {
			sawConflict = true
			if event.Actor != attacker {
				t.Fatalf("conflict actor = %+v, want %+v", event.Actor, attacker)
			}
			if event.IncomingDigest != ComputeDigest(tampered) {
				t.Fatalf("conflict incoming digest = %s, want %s", event.IncomingDigest, ComputeDigest(tampered))
			}
		}
	}
	if !sawConflict {
		t.Fatal("provenance chain does not record the rejected overwrite")
	}
}

func TestDigestMismatchIsDetectedBeforeBytesAreReleased(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	payload := []byte("original artifact bytes")
	originalDigest := ComputeDigest(payload)
	builder := testSource("build")
	publisher := testSource("publish")
	const name = "release/app.tgz"

	artifact, err := store.UploadBytes(ctx, builder, name, payload)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if artifact.Digest != originalDigest {
		t.Fatalf("digest = %s, want %s", artifact.Digest, originalDigest)
	}

	blob := store.blobPath(originalDigest)
	if err := os.Chmod(blob, 0o600); err != nil {
		t.Fatalf("chmod blob: %v", err)
	}
	tampered := bytes.Repeat([]byte("x"), len(payload))
	if err := os.WriteFile(blob, tampered, 0o600); err != nil {
		t.Fatalf("tamper blob: %v", err)
	}

	var destination bytes.Buffer
	_, err = store.Download(ctx, publisher, name, &destination)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Download error = %v, want ErrDigestMismatch", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("Download released %d bytes before verification", destination.Len())
	}

	provenance, provErr := store.Provenance(ctx, publisher, name)
	if provErr != nil {
		t.Fatalf("Provenance: %v", provErr)
	}
	var sawIntegrityFailure bool
	for _, event := range provenance.Events {
		if event.Kind == EventIntegrityFailure {
			sawIntegrityFailure = true
			if event.Digest != originalDigest {
				t.Fatalf("integrity event digest = %s, want %s", event.Digest, originalDigest)
			}
		}
	}
	if !sawIntegrityFailure {
		t.Fatal("provenance chain does not record the integrity failure")
	}

	// Re-uploading the verified original bytes is idempotent and repairs the
	// content-addressed blob without changing the name binding.
	if _, err := store.UploadBytes(ctx, builder, name, payload); err != nil {
		t.Fatalf("repairing Upload: %v", err)
	}
	repaired, repairedArtifact, err := store.DownloadBytes(ctx, publisher, name)
	if err != nil {
		t.Fatalf("Download after repair: %v", err)
	}
	if !bytes.Equal(repaired, payload) || repairedArtifact.Digest != originalDigest {
		t.Fatalf("repaired download = %q digest %s, want %q digest %s", repaired, repairedArtifact.Digest, payload, originalDigest)
	}
}

func TestUploadExpectedDigestMismatchIsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	payload := []byte("actual bytes")
	wrong := ComputeDigest([]byte("different bytes"))

	if _, err := store.UploadBytes(ctx, builder, "artifact", payload, WithExpectedDigest(wrong)); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Upload error = %v, want ErrDigestMismatch", err)
	}
	if _, err := store.Stat(ctx, testSource("publish"), "artifact"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat after rejected upload error = %v, want ErrNotFound", err)
	}
}

func TestCrossRunReadIsDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	otherRun := testSource("publish")
	otherRun.RunID = "run-2"
	const name = "release/app.tgz"

	if _, err := store.UploadBytes(ctx, builder, name, []byte("run one artifact")); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	var destination bytes.Buffer
	_, err := store.Download(ctx, otherRun, name, &destination)
	if !errors.Is(err, ErrCrossRun) {
		t.Fatalf("Download error = %v, want ErrCrossRun", err)
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Download error = %v, want it to also match ErrForbidden", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("cross-run download released %d bytes", destination.Len())
	}
}

func TestCrossWorkspaceReadIsDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	otherWorkspace := testSource("publish")
	otherWorkspace.Workspace = "workspace-b"
	const name = "release/app.tgz"

	if _, err := store.UploadBytes(ctx, builder, name, []byte("workspace-a artifact")); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	var destination bytes.Buffer
	_, err := store.Download(ctx, otherWorkspace, name, &destination)
	if !errors.Is(err, ErrCrossWorkspace) {
		t.Fatalf("Download error = %v, want ErrCrossWorkspace", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("cross-workspace download released %d bytes", destination.Len())
	}
}

func TestExpiredArtifactCannotBeReadOrReclaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	store := newTestStore(t, WithClock(clock))
	builder := testSource("build")
	attacker := testSource("untrusted-test")
	publisher := testSource("publish")
	const name = "release/app.tgz"

	artifact, err := store.UploadBytes(ctx, builder, name, []byte("retained artifact"), WithExpiresAt(now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if artifact.ExpiresAt.IsZero() {
		t.Fatal("Upload did not record an expiry")
	}

	if _, err := store.Fetch(ctx, publisher, name); err != nil {
		t.Fatalf("Fetch before expiry: %v", err)
	}

	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()

	var destination bytes.Buffer
	_, err = store.Download(ctx, publisher, name, &destination)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Download after expiry error = %v, want ErrExpired", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("expired download released %d bytes", destination.Len())
	}

	// Expiry removes read access but must not release the name for takeover.
	if _, err := store.UploadBytes(ctx, attacker, name, []byte("replacement artifact")); !errors.Is(err, ErrConflict) {
		t.Fatalf("post-expiry overwrite error = %v, want ErrConflict", err)
	}
}

func TestStoreReopenPreservesBindingsAndProvenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	builder := testSource("build")
	attacker := testSource("untrusted-test")
	publisher := testSource("publish")
	const name = "release/app.tgz"
	payload := []byte("persistent artifact")

	first, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := first.UploadBytes(ctx, builder, name, payload); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, _, err := first.DownloadBytes(ctx, publisher, name); err != nil {
		t.Fatalf("Download: %v", err)
	}

	second, err := NewStore(root)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	got, artifact, err := second.DownloadBytes(ctx, publisher, name)
	if err != nil {
		t.Fatalf("reopened DownloadBytes: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reopened bytes = %q, want %q", got, payload)
	}
	if artifact.Source != builder {
		t.Fatalf("reopened source = %+v, want %+v", artifact.Source, builder)
	}
	if _, err := second.UploadBytes(ctx, attacker, name, []byte("different")); !errors.Is(err, ErrConflict) {
		t.Fatalf("reopened overwrite error = %v, want ErrConflict", err)
	}
	provenance, err := second.Provenance(ctx, publisher, name)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if len(provenance.Events) != 4 {
		t.Fatalf("events = %d, want upload, download, conflict and reopened download", len(provenance.Events))
	}
}

func TestConcurrentSameNameUploadHasSingleWinner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	const name = "release/app.tgz"
	contents := [][]byte{[]byte("first"), []byte("second"), []byte("third")}

	type result struct {
		artifact *Artifact
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(contents))
	var wg sync.WaitGroup
	for _, content := range contents {
		content := content
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			artifact, err := store.UploadBytes(ctx, builder, name, content)
			results <- result{artifact: artifact, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes int
	var winner Digest
	var conflicts int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.artifact.Digest
		case errors.Is(result.err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected upload error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != len(contents)-1 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and %d", successes, conflicts, len(contents)-1)
	}
	got, artifact, err := store.DownloadBytes(ctx, testSource("publish"), name)
	if err != nil {
		t.Fatalf("DownloadBytes: %v", err)
	}
	if artifact.Digest != winner || ComputeDigest(got) != winner {
		t.Fatalf("winner digest = %s, downloaded = %s/%s", winner, artifact.Digest, ComputeDigest(got))
	}
}

func TestSameContentUnderDifferentNamesIsContentAddressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	payload := []byte("shared artifact bytes")
	digest := ComputeDigest(payload)

	for _, name := range []string{"release/a.tgz", "release/b.tgz"} {
		if _, err := store.UploadBytes(ctx, builder, name, payload); err != nil {
			t.Fatalf("Upload %s: %v", name, err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(store.blobDir, "**", string(digest)))
	if err != nil {
		t.Fatalf("glob blobs: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("found %d blobs for digest %s, want 1 (%v)", len(matches), digest, matches)
	}
	if _, err := store.FindByDigest(ctx, testSource("publish"), digest); err != nil {
		t.Fatalf("FindByDigest: %v", err)
	}
}

func TestDigestLookupAndOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	payload := []byte("streamed artifact bytes")
	digest := ComputeDigest(payload)

	if _, err := store.UploadBytes(ctx, builder, "release/app.tgz", payload); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	got, err := store.FetchDigest(ctx, testSource("publish"), digest)
	if err != nil {
		t.Fatalf("FetchDigest: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("FetchDigest bytes = %q, want %q", got, payload)
	}

	reader, artifact, err := store.OpenDigest(ctx, testSource("publish"), digest)
	if err != nil {
		t.Fatalf("OpenDigest: %v", err)
	}
	if artifact.Digest != digest {
		t.Fatalf("OpenDigest artifact digest = %s, want %s", artifact.Digest, digest)
	}
	opened, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read opened artifact: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close opened artifact: %v", err)
	}
	if !bytes.Equal(opened, payload) {
		t.Fatalf("OpenDigest bytes = %q, want %q", opened, payload)
	}
	entries, err := os.ReadDir(store.tmpDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("verified open leaked %d temporary files", len(entries))
	}
}

func TestUploadSizeLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t, WithMaxArtifactSize(4))
	if _, err := store.UploadBytes(ctx, testSource("build"), "too-large", []byte("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Upload error = %v, want ErrTooLarge", err)
	}
	if _, err := store.UploadBytes(ctx, testSource("build"), "fits", []byte("1234")); err != nil {
		t.Fatalf("Upload within limit: %v", err)
	}
}

func TestScopeMismatchIsDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	builder := testSource("build")
	otherScope := testSource("publish")
	otherScope.Scope = "staging"
	const name = "release/app.tgz"

	if _, err := store.UploadBytes(ctx, builder, name, []byte("release scoped artifact")); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	var destination bytes.Buffer
	_, err := store.Download(ctx, otherScope, name, &destination)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Download error = %v, want ErrForbidden", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("cross-scope download released %d bytes", destination.Len())
	}
}

func TestParseDigest(t *testing.T) {
	t.Parallel()
	canonical := ComputeDigest([]byte("payload"))
	for _, input := range []string{string(canonical), "sha256:" + string(canonical), "SHA256:" + string(canonical)} {
		digest, err := ParseDigest(input)
		if err != nil {
			t.Fatalf("ParseDigest(%q): %v", input, err)
		}
		if digest != canonical {
			t.Fatalf("ParseDigest(%q) = %s, want %s", input, digest, canonical)
		}
	}
	if _, err := ParseDigest("not-a-digest"); err == nil {
		t.Fatal("ParseDigest accepted invalid input")
	}
}
