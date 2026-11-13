package netsec

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		t.Fatalf("write %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func TestOSFreezeAndRead(t *testing.T) {
	base := t.TempDir()
	modelDir := filepath.Join(base, "model")
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	writeTestFile(t, filepath.Join(modelDir, "weights.bin"), data)

	a := initForRange("a", "weights.bin", data, 0, 4, Uint8, []int64{4})
	model, err := Freeze(Spec{
		Root:         modelDir,
		GraphDigest:  SumDigest([]byte("graph")),
		Initializers: []Initializer{a},
	})
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	defer model.Close()

	got, err := model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor: %v", err)
	}
	if !bytes.Equal(got, data[:4]) {
		t.Fatalf("Tensor = %v, want %v", got, data[:4])
	}

	// The frozen handle still reads the original inode after the path is
	// replaced by an attacker-controlled regular file.
	sentinel := []byte("HOST-ROOT-SENTINEL")
	replacement := filepath.Join(modelDir, "replacement.bin")
	writeTestFile(t, replacement, sentinel)
	if err := os.Rename(replacement, filepath.Join(modelDir, "weights.bin")); err != nil {
		t.Fatalf("replace weight path: %v", err)
	}
	got, err = model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor after path replacement: %v", err)
	}
	if !bytes.Equal(got, data[:4]) {
		t.Fatalf("Tensor after replacement = %v, want original", got)
	}

	// Deleting and recreating the model directory path must not redirect
	// reads either.
	if err := os.RemoveAll(modelDir); err != nil {
		t.Fatalf("remove model dir: %v", err)
	}
	if err := os.Mkdir(modelDir, 0o755); err != nil {
		t.Fatalf("recreate model dir: %v", err)
	}
	writeTestFile(t, filepath.Join(modelDir, "weights.bin"), sentinel)
	got, err = model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor after directory recreation: %v", err)
	}
	if !bytes.Equal(got, data[:4]) {
		t.Fatalf("Tensor after directory recreation = %v, want original", got)
	}
}

func TestOSSymlinkRejected(t *testing.T) {
	base := t.TempDir()
	modelDir := filepath.Join(base, "model")
	hostDir := filepath.Join(base, "host")
	sentinel := []byte("HOST-ROOT-SENTINEL")
	writeTestFile(t, filepath.Join(hostDir, "credential"), sentinel)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(filepath.Join(hostDir, "credential"), filepath.Join(modelDir, "weights.bin")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	a := Initializer{
		TensorID: "a",
		Location: "weights.bin",
		Length:   int64(len(sentinel)),
		SHA256:   SumDigest(sentinel),
		Dtype:    Uint8,
		Shape:    []int64{int64(len(sentinel))},
	}
	_, err := Freeze(Spec{Root: modelDir, Initializers: []Initializer{a}})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink Freeze error = %v, want ErrSymlink", err)
	}
}

func TestOSHardlinkRejected(t *testing.T) {
	base := t.TempDir()
	modelDir := filepath.Join(base, "model")
	hostDir := filepath.Join(base, "host")
	sentinel := []byte("HOST-ROOT-SENTINEL")
	writeTestFile(t, filepath.Join(modelDir, "weights.bin"), sentinel)
	writeTestFile(t, filepath.Join(hostDir, "credential"), sentinel)
	if err := os.Link(filepath.Join(hostDir, "credential"), filepath.Join(modelDir, "weights-link.bin")); err != nil {
		t.Fatalf("Link: %v", err)
	}

	a := Initializer{
		TensorID: "a",
		Location: "weights-link.bin",
		Length:   int64(len(sentinel)),
		SHA256:   SumDigest(sentinel),
		Dtype:    Uint8,
		Shape:    []int64{int64(len(sentinel))},
	}
	_, err := Freeze(Spec{Root: modelDir, Initializers: []Initializer{a}})
	if !errors.Is(err, ErrHardlink) {
		t.Fatalf("hardlink Freeze error = %v, want ErrHardlink", err)
	}
}

func TestOSTruncationInvalidates(t *testing.T) {
	base := t.TempDir()
	modelDir := filepath.Join(base, "model")
	path := filepath.Join(modelDir, "weights.bin")
	data := []byte{1, 2, 3, 4}
	writeTestFile(t, path, data)

	a := initForRange("a", "weights.bin", data, 0, 4, Uint8, []int64{4})
	model, err := Freeze(Spec{Root: modelDir, Initializers: []Initializer{a}})
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	defer model.Close()
	if err := os.Truncate(path, 1); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if _, err := model.Tensor("a"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor after truncation = %v, want ErrModelInvalid", err)
	}
}

func TestOSSymlinkInIntermediateDirectoryRejected(t *testing.T) {
	base := t.TempDir()
	modelDir := filepath.Join(base, "model")
	hostDir := filepath.Join(base, "host")
	sentinel := []byte("HOST-ROOT-SENTINEL")
	writeTestFile(t, filepath.Join(hostDir, "weights.bin"), sentinel)
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(hostDir, filepath.Join(modelDir, "layers")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	a := Initializer{
		TensorID: "a",
		Location: "layers/weights.bin",
		Length:   int64(len(sentinel)),
		SHA256:   SumDigest(sentinel),
		Dtype:    Uint8,
		Shape:    []int64{int64(len(sentinel))},
	}
	_, err := Freeze(Spec{Root: modelDir, Initializers: []Initializer{a}})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("intermediate symlink error = %v, want ErrSymlink", err)
	}
}
