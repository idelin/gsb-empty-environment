package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRoundtripAndVerify(t *testing.T) {
	dir := t.TempDir()
	in1 := filepath.Join(dir, "a.bin")
	in2 := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(in1, []byte("hello archive world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in2, make([]byte, 10000), 0o644); err != nil {
		t.Fatal(err)
	}
	arc, err := CreateArchive([]string{in1, in2}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	// 19 bytes -> 1 block; 10000 bytes -> 3 blocks (4096+4096+1808).
	if len(arc.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(arc.Blocks))
	}
	path := filepath.Join(dir, "test.arc")
	if err := SaveArchive(arc, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TotalLength().Int64() != 19+10000 {
		t.Fatalf("total length %s, want 10019", loaded.TotalLength())
	}
	for i := range loaded.Blocks {
		if !VerifyBlock(&loaded.Blocks[i]) {
			t.Fatalf("block %d failed verification", i)
		}
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	arc, err := CreateArchive([]string{writeTemp(t, "payload data")}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	arc.Blocks[0].Data[0] ^= 0xFF
	if VerifyBlock(&arc.Blocks[0]) {
		t.Fatal("tampered block passed verification")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
