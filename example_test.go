package cdcupload_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"example.org/cdcupload"
)

// Demonstrates uploading a large file, re-uploading a slightly modified
// version, and observing content-defined chunk reuse plus exact restoration.
func ExampleUploadFile_versionedReuse() {
	ctx := context.Background()
	cfg := cdcupload.ChunkConfig{MinSize: 1024, AvgSize: 4096, MaxSize: 16384}

	work, _ := os.MkdirTemp("", "cdc-example-*")
	defer os.RemoveAll(work)

	rng := rand.New(rand.NewSource(1))
	base := make([]byte, 128*1024)
	rng.Read(base)

	remote, _ := cdcupload.NewRemoteStore(filepath.Join(work, "remote"))
	stateDir := filepath.Join(work, "state")

	v1Path := filepath.Join(work, "events.log")
	_ = os.WriteFile(v1Path, base, 0o644)
	m1, _ := cdcupload.UploadFile(ctx, remote, stateDir, v1Path, cfg)

	// Insert 128 bytes at a random position to simulate a small local edit.
	pos := rng.Intn(len(base))
	insert := make([]byte, 128)
	rng.Read(insert)
	version := append(append(append([]byte{}, base[:pos]...), insert...), base[pos:]...)
	v2Path := filepath.Join(work, "events-v2.log")
	_ = os.WriteFile(v2Path, version, 0o644)
	m2, _ := cdcupload.UploadFile(ctx, remote, stateDir, v2Path, cfg)

	var restored bytes.Buffer
	_ = cdcupload.Restore(ctx, remote, m2, &restored)

	fmt.Println("v1 chunks:", m1.ChunkSize)
	fmt.Println("v2 chunks:", m2.ChunkSize)
	fmt.Println("restored identical:", bytes.Equal(restored.Bytes(), version))
	// Output:
	// v1 chunks: 45
	// v2 chunks: 45
	// restored identical: true
}
