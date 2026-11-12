package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func pointerFor(content []byte) Pointer {
	return Pointer{
		Version: PointerVersion,
		OID:     objectID(content),
		Size:    int64(len(content)),
	}
}

func TestPublishObjectAtomicallyReplacesDestination(t *testing.T) {
	content := []byte("verified object contents")
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")

	if err := os.WriteFile(dest, []byte("old contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishObject(context.Background(), bytes.NewReader(content), dest, pointerFor(content)); err != nil {
		t.Fatalf("PublishObject() error = %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("destination = %q, want %q", got, content)
	}
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectEmptyFile(t *testing.T) {
	content := []byte{}
	dir := t.TempDir()
	dest := filepath.Join(dir, "empty")

	if err := PublishObject(context.Background(), bytes.NewReader(content), dest, pointerFor(content)); err != nil {
		t.Fatalf("PublishObject() error = %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("destination has %d bytes, want 0", len(got))
	}
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectRejectsLengthMismatch(t *testing.T) {
	expected := []byte("expected")
	tests := []struct {
		name     string
		response []byte
	}{
		{name: "too short", response: []byte("exp")},
		{name: "too long", response: []byte("expected plus extra bytes")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "object")
			sentinel := []byte("existing verified object")
			if err := os.WriteFile(dest, sentinel, 0o600); err != nil {
				t.Fatal(err)
			}

			err := PublishObject(context.Background(), bytes.NewReader(test.response), dest, pointerFor(expected))
			if !errors.Is(err, ErrObjectSizeMismatch) {
				t.Fatalf("PublishObject() error = %v, want ErrObjectSizeMismatch", err)
			}
			assertFileContents(t, dest, sentinel)
			assertNoTemporaryFiles(t, dir)
		})
	}
}

func TestPublishObjectRejectsDigestMismatch(t *testing.T) {
	expected := []byte("aaaaaa")
	response := []byte("bbbbbb")
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")
	sentinel := []byte("existing verified object")
	if err := os.WriteFile(dest, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}

	err := PublishObject(context.Background(), bytes.NewReader(response), dest, pointerFor(expected))
	if !errors.Is(err, ErrObjectDigestMismatch) {
		t.Fatalf("PublishObject() error = %v, want ErrObjectDigestMismatch", err)
	}
	assertFileContents(t, dest, sentinel)
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectRejectsMidStreamReplacement(t *testing.T) {
	expected := []byte("0123456789abcdef")
	replacement := []byte("0123456789ABCDEF")
	switched := &chunkReader{
		chunks: [][]byte{
			replacement[:8],
			replacement[8:],
		},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")

	err := PublishObject(context.Background(), switched, dest, pointerFor(expected))
	if !errors.Is(err, ErrObjectDigestMismatch) {
		t.Fatalf("PublishObject() error = %v, want ErrObjectDigestMismatch", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after failed publication: %v", statErr)
	}
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectCleansUpAfterReadError(t *testing.T) {
	expected := []byte("expected")
	readErr := errors.New("response interrupted")
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")

	reader := &chunkReader{
		chunks: [][]byte{[]byte("exp")},
		err:    readErr,
	}
	err := PublishObject(context.Background(), reader, dest, pointerFor(expected))
	if !errors.Is(err, readErr) {
		t.Fatalf("PublishObject() error = %v, want wrapped read error", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after failed publication: %v", statErr)
	}
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectHonorsCancellation(t *testing.T) {
	expected := []byte("expected")
	ctx, cancel := context.WithCancel(context.Background())
	reader := &chunkReader{
		chunks: [][]byte{[]byte("exp")},
		afterRead: func() {
			cancel()
		},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")

	err := PublishObject(ctx, reader, dest, pointerFor(expected))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishObject() error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after cancellation: %v", statErr)
	}
	assertNoTemporaryFiles(t, dir)
}

func TestPublishObjectRejectsMalformedExpectedPointer(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "object")

	tests := []struct {
		name     string
		expected Pointer
	}{
		{
			name:     "bad oid",
			expected: Pointer{OID: "sha256:xyz", Size: 1},
		},
		{
			name:     "negative size",
			expected: Pointer{OID: objectID(nil), Size: -1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := PublishObject(context.Background(), bytes.NewReader(nil), dest, test.expected)
			if !errors.Is(err, ErrInvalidPointer) {
				t.Fatalf("PublishObject() error = %v, want ErrInvalidPointer", err)
			}
			assertNoTemporaryFiles(t, dir)
		})
	}
}

type chunkReader struct {
	chunks    [][]byte
	index     int
	err       error
	afterRead func()
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.index])
	r.index++
	if r.afterRead != nil {
		r.afterRead()
	}
	return n, nil
}

func assertFileContents(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertNoTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) >= len(".lfs-download-") && entry.Name()[:len(".lfs-download-")] == ".lfs-download-" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestCrossRepositoryOIDCannotSelectObject(t *testing.T) {
	repoAContent := []byte("repo-a object")
	repoBContent := []byte("repo-b object")
	allowed := NewOIDSet(objectID(repoAContent))

	pointer, err := ValidatePointer([]byte(canonicalPointer(repoBContent)), allowed)
	if !errors.Is(err, ErrOIDNotAuthorized) {
		t.Fatalf("ValidatePointer() error = %v, want ErrOIDNotAuthorized", err)
	}
	if pointer != (Pointer{}) {
		t.Fatalf("pointer = %+v, want zero value on authorization failure", pointer)
	}

	dir := t.TempDir()
	dest := filepath.Join(dir, "object")
	err = PublishObject(context.Background(), bytes.NewReader(repoBContent), dest, pointer)
	if !errors.Is(err, ErrInvalidPointer) {
		t.Fatalf("PublishObject() with rejected pointer error = %v, want ErrInvalidPointer", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after rejected pointer: %v", statErr)
	}
}
