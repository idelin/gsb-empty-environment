package cdcupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRand(t *testing.T, seed int64) *rand.Rand {
	t.Helper()
	return rand.New(rand.NewSource(seed))
}

func TestCorruptSessionRejected(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := newRand(t, 1)
	data := make([]byte, 32*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "corrupt-session.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")

	// Create a valid session first.
	flaky := &FlakyUploader{store: remote, failUntil: 2}
	_, _ = UploadFile(ctx, flaky, stateDir, filePath, cfg)

	sessionFiles, _ := os.ReadDir(stateDir)
	if len(sessionFiles) != 1 {
		t.Fatalf("expected one session, got %d", len(sessionFiles))
	}
	sessionFile := filepath.Join(stateDir, sessionFiles[0].Name())

	original, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionFile, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UploadFile(ctx, remote, stateDir, filePath, cfg); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("expected ErrSessionCorrupt, got %v", err)
	}

	// A repaired session resumes normally.
	if err := os.WriteFile(sessionFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := UploadFile(ctx, remote, stateDir, filePath, cfg)
	if err != nil {
		t.Fatalf("resume after repaired session: %v", err)
	}
	var restored bytes.Buffer
	if err := Restore(ctx, remote, manifest, &restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Bytes(), data) {
		t.Fatal("integrity mismatch")
	}
}

func TestManifestValidationErrors(t *testing.T) {
	good := &Manifest{
		FileID:    "f",
		Size:      4,
		FileHash:  strings.Repeat("a", 64),
		ChunkSize: 1,
		Chunks: []ChunkEntry{{
			Index:  0,
			Offset: 0,
			Length: 4,
			Digest: strings.Repeat("b", 64),
		}},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	bad := *good
	bad.ChunkSize = 2
	if err := bad.Validate(); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("expected corrupt error, got %v", err)
	}

	bad = *good
	bad.Chunks[0].Index = 9
	if err := bad.Validate(); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("expected corrupt error, got %v", err)
	}

	bad = *good
	bad.Size = 5
	if err := bad.Validate(); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("expected corrupt error, got %v", err)
	}

	bad = *good
	bad.Chunks[0].Digest = "short"
	if err := bad.Validate(); !errors.Is(err, ErrSessionCorrupt) {
		t.Fatalf("expected corrupt error, got %v", err)
	}
}

func TestNextWrapperAndReaderError(t *testing.T) {
	cfg := testConfig()
	data := bytes.Repeat([]byte{1, 2, 3, 4}, 4096)
	c, err := NewChunker(bytes.NewReader(data), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ch.Length <= 0 {
		t.Fatal("empty first chunk")
	}

	// Reading past the end repeatedly returns io.EOF.
	for {
		if _, err := c.Next(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestPutChunkDigestMismatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.PutChunk(ctx, strings.Repeat("c", 64), []byte("data")); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("expected integrity error, got %v", err)
	}
}

func TestRestoreRejectsTamperedBlob(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	rng := newRand(t, 2)
	data := make([]byte, 32*1024)
	rng.Read(data)
	filePath := writeTempFile(t, "tamper.bin", data)

	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := UploadFile(ctx, remote, filepath.Join(root, "state"), filePath, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper a stored blob.
	blob := filepath.Join(remote.root, "blobs", manifest.Chunks[0].Digest)
	raw, err := os.ReadFile(blob)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xff
	if err := os.WriteFile(blob, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Restore(ctx, remote, manifest, &out); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("expected ErrIntegrity, got %v", err)
	}
}

func TestUploadMissingFile(t *testing.T) {
	root := t.TempDir()
	remote, err := NewRemoteStore(filepath.Join(root, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UploadFile(context.Background(), remote, filepath.Join(root, "state"),
		filepath.Join(root, "does-not-exist"), testConfig()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}
