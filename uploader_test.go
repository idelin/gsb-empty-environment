package cdcupload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// errInjected is returned by FlakyUploader when a call is forced to fail.
var errInjected = errors.New("simulated transport failure")

// FlakyUploader wraps a RemoteStore and fails PutChunk calls until a target
// number of chunks has been stored, modeling an upload process that crashes
// mid-transfer and is restarted with the same on-disk session state.
type FlakyUploader struct {
	store     *RemoteStore
	failUntil int // fail while stored chunks < failUntil
	failures  int
}

func (f *FlakyUploader) HasChunk(ctx context.Context, digest string) (bool, error) {
	return f.store.HasChunk(ctx, digest)
}

func (f *FlakyUploader) PutChunk(ctx context.Context, digest string, data []byte) error {
	if f.failures < f.failUntil {
		f.failures++
		return errInjected
	}
	return f.store.PutChunk(ctx, digest, data)
}

func (f *FlakyUploader) CommitManifest(ctx context.Context, m *Manifest) error {
	return f.store.CommitManifest(ctx, m)
}

func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestUploadResumeAndDedup(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := rand.New(rand.NewSource(99))
	data := make([]byte, 128*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "sample.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")

	// First attempt: fail the first several chunk uploads.
	flaky := &FlakyUploader{store: remote, failUntil: 5}
	_, err = UploadFile(ctx, flaky, stateDir, filePath, cfg)
	if !errors.Is(err, errInjected) {
		t.Fatalf("expected injected failure, got %v", err)
	}

	// No manifest may be visible before the upload completes.
	if _, err := os.Stat(filepath.Join(remote.root, "manifests")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(remote.root, "manifests"))
	if len(entries) != 0 {
		t.Fatal("manifest visible despite incomplete upload")
	}

	// Session state must exist and record only fully uploaded chunks.
	sessionFiles, _ := os.ReadDir(stateDir)
	if len(sessionFiles) != 1 {
		t.Fatalf("expected one session file, got %d", len(sessionFiles))
	}

	// Resume with a reliable transport until completion.
	manifest, err := UploadFile(ctx, remote, stateDir, filePath, cfg)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if manifest.Size != int64(len(data)) || manifest.ChunkSize == 0 {
		t.Fatalf("bad manifest: %+v", manifest)
	}

	// Re-running must be fully idempotent: no new chunk blobs are written.
	putsBefore, _ := remote.Stats()
	manifest2, err := UploadFile(ctx, remote, stateDir, filePath, cfg)
	if err != nil {
		t.Fatalf("re-upload failed: %v", err)
	}
	putsAfter, commitsAfter := remote.Stats()
	if putsAfter != putsBefore {
		t.Fatalf("re-upload wrote %d new chunks despite dedup", putsAfter-putsBefore)
	}
	if manifest2.FileID != manifest.FileID || commitsAfter != 1 {
		t.Fatal("manifest should be committed exactly once")
	}

	// Restore and verify byte-for-byte integrity.
	var restored bytes.Buffer
	if err := Restore(ctx, remote, manifest, &restored); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !bytes.Equal(restored.Bytes(), data) {
		t.Fatal("restored content differs from source")
	}

	// Persisted manifest on disk must match what was returned.
	onDisk, err := remote.ReadManifest(manifest.FileID)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if onDisk.ChunkSize != manifest.ChunkSize || onDisk.FileHash != manifest.FileHash {
		t.Fatal("persisted manifest mismatch")
	}
}

func TestVersionedReuseRate(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := rand.New(rand.NewSource(2026))
	base := make([]byte, 256*1024)
	rng.Read(base)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}

	basePath := writeTempFile(t, "v1.log", base)
	m1, err := UploadFile(ctx, remote, filepath.Join(root, "state"), basePath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	putsV1, _ := remote.Stats()

	// Apply 8 small random insertions at different positions.
	version := base
	for i := 0; i < 8; i++ {
		version = insertAtRandom(version, rng, 200)
	}
	v2Path := writeTempFile(t, "v2.log", version)
	m2, err := UploadFile(ctx, remote, filepath.Join(root, "state"), v2Path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	putsV2, _ := remote.Stats()

	shared := sharedManifestChunks(m1, m2)
	reuse := float64(shared) / float64(m2.ChunkSize)
	t.Logf("v1 chunks=%d v2 chunks=%d reused=%d reuse=%.3f new blobs uploaded=%d (v1=%d)",
		m1.ChunkSize, m2.ChunkSize, shared, reuse, putsV2-putsV1, putsV1)
	if reuse < 0.8 {
		t.Fatalf("cross-version chunk reuse too low: %.3f", reuse)
	}
	// Every reused chunk must have been deduplicated against the remote.
	newBlobs := putsV2 - putsV1
	if newBlobs != m2.ChunkSize-shared {
		t.Fatalf("uploaded %d blobs but only %d chunks are new", newBlobs, m2.ChunkSize-shared)
	}

	var restored bytes.Buffer
	if err := Restore(ctx, remote, m2, &restored); err != nil {
		t.Fatalf("restore v2: %v", err)
	}
	if !bytes.Equal(restored.Bytes(), version) {
		t.Fatal("restored v2 content differs")
	}
}

func sharedManifestChunks(a, b *Manifest) int {
	seen := make(map[string]int)
	for _, ch := range a.Chunks {
		seen[ch.Digest]++
	}
	shared := 0
	for _, ch := range b.Chunks {
		if seen[ch.Digest] > 0 {
			seen[ch.Digest]--
			shared++
		}
	}
	return shared
}

func TestResumeAcrossMultipleCrashes(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := rand.New(rand.NewSource(555))
	data := make([]byte, 96*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "crashy.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")

	// Crash repeatedly at different progress points.
	for _, failUntil := range []int{2, 7, 13} {
		flaky := &FlakyUploader{store: remote, failUntil: failUntil}
		_, err := UploadFile(ctx, flaky, stateDir, filePath, cfg)
		if !errors.Is(err, errInjected) {
			t.Fatalf("attempt failUntil=%d: expected injected failure, got %v", failUntil, err)
		}
	}

	manifest, err := UploadFile(ctx, remote, stateDir, filePath, cfg)
	if err != nil {
		t.Fatalf("final resume: %v", err)
	}
	var restored bytes.Buffer
	if err := Restore(ctx, remote, manifest, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), data) {
		t.Fatal("integrity broken after multiple crash/resume cycles")
	}
}

func TestEmptyAndTinyFiles(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}

	for _, data := range [][]byte{{}, []byte("x"), bytes.Repeat([]byte("ab"), 100)} {
		path := writeTempFile(t, "small.bin", data)
		manifest, err := UploadFile(ctx, remote, filepath.Join(root, "state"), path, cfg)
		if err != nil {
			t.Fatalf("upload %d bytes: %v", len(data), err)
		}
		if len(data) == 0 {
			if manifest.ChunkSize != 0 {
				t.Fatal("empty file must produce zero chunks")
			}
			continue
		}
		var restored bytes.Buffer
		if err := Restore(ctx, remote, manifest, &restored); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(restored.Bytes(), data) {
			t.Fatalf("restored mismatch for %d-byte file", len(data))
		}
	}
}

func TestSourceChangeRejected(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := rand.New(rand.NewSource(11))
	data := make([]byte, 64*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "changing.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")

	flaky := &FlakyUploader{store: remote, failUntil: 3}
	if _, err := UploadFile(ctx, flaky, stateDir, filePath, cfg); !errors.Is(err, errInjected) {
		t.Fatalf("expected injected failure, got %v", err)
	}

	// Mutate the source in place before resuming.
	tweaked := append([]byte(nil), data...)
	tweaked[0] ^= 0xff
	if err := os.WriteFile(filePath, tweaked, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UploadFile(ctx, remote, stateDir, filePath, cfg); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("expected ErrSourceChanged, got %v", err)
	}
}

func TestManifestAtomicCommit(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := rand.New(rand.NewSource(33))
	data := make([]byte, 64*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "atomic.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}

	// Fail during chunk uploads: no temp or manifest artifacts may remain.
	flaky := &FlakyUploader{store: remote, failUntil: 100}
	_, _ = UploadFile(ctx, flaky, filepath.Join(root, "state"), filePath, cfg)

	walkErr := filepath.Walk(remote.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".tmp" ||
			len(filepath.Base(path)) > 0 && filepath.Base(path)[0] == '.') {
			return fmt.Errorf("leftover temp artifact: %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}

	manifest, err := UploadFile(ctx, remote, filepath.Join(root, "state"), filePath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ReadManifest(manifest.FileID); err != nil {
		t.Fatalf("committed manifest not readable: %v", err)
	}
}
