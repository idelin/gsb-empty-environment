package modverify

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testModule  = "example.org/m"
	testVersion = "v1.0.0"
	testPrefix  = testModule + "@" + testVersion + "/"
)

var baseFiles = map[string]string{
	testPrefix + "go.mod":   "module " + testModule + "\n\ngo 1.24.0\n",
	testPrefix + "main.go":  "package m\n\nfunc Hello() string { return \"hi\" }\n",
	testPrefix + "lib/a.go": "package lib\n",
}

func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mod.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGoMod(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(path, []byte(baseFiles[testPrefix+"go.mod"]), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGoSum(t *testing.T, zipHash, modHash string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go.sum")
	content := testModule + " " + testVersion + " " + zipHash + "\n" +
		testModule + " " + testVersion + "/go.mod " + modHash + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func baseHashes(t *testing.T) (zipPath, modPath, zipHash, modHash string) {
	t.Helper()
	zipPath = writeZip(t, baseFiles)
	modPath = writeGoMod(t)
	var err error
	if zipHash, err = HashZip(zipPath, testModule, testVersion); err != nil {
		t.Fatal(err)
	}
	if modHash, err = HashMod(modPath); err != nil {
		t.Fatal(err)
	}
	return
}

func TestHashZipSortedAndStable(t *testing.T) {
	_, _, zipHash, _ := baseHashes(t)
	if !strings.HasPrefix(zipHash, "h1:") {
		t.Fatalf("hash %q lacks h1: prefix", zipHash)
	}
	// Same content, different insertion order in the zip writer.
	reordered := map[string]string{
		testPrefix + "lib/a.go": baseFiles[testPrefix+"lib/a.go"],
		testPrefix + "main.go":  baseFiles[testPrefix+"main.go"],
		testPrefix + "go.mod":   baseFiles[testPrefix+"go.mod"],
	}
	again, err := HashZip(writeZip(t, reordered), testModule, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if again != zipHash {
		t.Fatalf("hash not order-independent: %q vs %q", again, zipHash)
	}
}

func TestHashZipMissingPrefix(t *testing.T) {
	files := map[string]string{
		testPrefix + "go.mod": baseFiles[testPrefix+"go.mod"],
		"evil.go":             "package main\n",
	}
	if _, err := HashZip(writeZip(t, files), testModule, testVersion); err == nil {
		t.Fatal("expected error for entry lacking module@version/ prefix")
	} else if !strings.Contains(err.Error(), "lacks required prefix") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHashZipNewlineRejected(t *testing.T) {
	files := map[string]string{
		testPrefix + "go.mod":       baseFiles[testPrefix+"go.mod"],
		testPrefix + "bad\nname.go": "package m\n",
	}
	if _, err := HashZip(writeZip(t, files), testModule, testVersion); err == nil {
		t.Fatal("expected error for newline in entry name")
	}
}

func TestVerifyModuleVerified(t *testing.T) {
	zipPath, modPath, zipHash, modHash := baseHashes(t)
	res, err := VerifyModule(VerifyOptions{
		ZipPath:   zipPath,
		ModPath:   modPath,
		Module:    testModule,
		Version:   testVersion,
		GoSumPath: writeGoSum(t, zipHash, modHash),
		DB:        &Reference{ZipHash: zipHash, ModHash: modHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Verified {
		t.Fatalf("got %v (%s), want verified", res.Status, res.Detail)
	}
}

func TestVerifyModuleSingleByteMismatch(t *testing.T) {
	_, _, zipHash, modHash := baseHashes(t)

	tampered := map[string]string{}
	for k, v := range baseFiles {
		tampered[k] = v
	}
	// Attacker flips a single byte inside main.go.
	body := []byte(tampered[testPrefix+"main.go"])
	body[len(body)/2] ^= 0x01
	tampered[testPrefix+"main.go"] = string(body)

	res, err := VerifyModule(VerifyOptions{
		ZipPath:   writeZip(t, tampered),
		ModPath:   writeGoMod(t),
		Module:    testModule,
		Version:   testVersion,
		GoSumPath: writeGoSum(t, zipHash, modHash),
		DB:        &Reference{ZipHash: zipHash, ModHash: modHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Mismatch {
		t.Fatalf("got %v, want mismatch", res.Status)
	}
}

func TestVerifyModuleExtraFile(t *testing.T) {
	_, _, zipHash, modHash := baseHashes(t)

	injected := map[string]string{}
	for k, v := range baseFiles {
		injected[k] = v
	}
	injected[testPrefix+"hack/backdoor.go"] = "package hack\n"

	res, err := VerifyModule(VerifyOptions{
		ZipPath:   writeZip(t, injected),
		ModPath:   writeGoMod(t),
		Module:    testModule,
		Version:   testVersion,
		GoSumPath: writeGoSum(t, zipHash, modHash),
		DB:        &Reference{ZipHash: zipHash, ModHash: modHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Mismatch {
		t.Fatalf("got %v, want mismatch", res.Status)
	}
}

func TestVerifyModulePendingReview(t *testing.T) {
	zipPath, modPath, zipHash, modHash := baseHashes(t)

	// Only go.sum available, no verification-database record.
	res, err := VerifyModule(VerifyOptions{
		ZipPath:   zipPath,
		ModPath:   modPath,
		Module:    testModule,
		Version:   testVersion,
		GoSumPath: writeGoSum(t, zipHash, modHash),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != PendingReview {
		t.Fatalf("go.sum only: got %v, want pending-review", res.Status)
	}

	// Only the verification database available, no go.sum.
	res, err = VerifyModule(VerifyOptions{
		ZipPath: zipPath,
		ModPath: modPath,
		Module:  testModule,
		Version: testVersion,
		DB:      &Reference{ZipHash: zipHash, ModHash: modHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != PendingReview {
		t.Fatalf("db only: got %v, want pending-review", res.Status)
	}
}

func TestVerifyModuleSingleSourceMismatchStillRejects(t *testing.T) {
	_, _, zipHash, modHash := baseHashes(t)

	tampered := map[string]string{}
	for k, v := range baseFiles {
		tampered[k] = v
	}
	tampered[testPrefix+"lib/a.go"] = "package lib // pwned\n"

	// DB record missing, but go.sum alone must still catch the tampering.
	res, err := VerifyModule(VerifyOptions{
		ZipPath:   writeZip(t, tampered),
		ModPath:   writeGoMod(t),
		Module:    testModule,
		Version:   testVersion,
		GoSumPath: writeGoSum(t, zipHash, modHash),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != Mismatch {
		t.Fatalf("got %v, want mismatch", res.Status)
	}
}

func TestHashModRecordedSeparately(t *testing.T) {
	_, _, zipHash, modHash := baseHashes(t)
	if zipHash == modHash {
		t.Fatal("zip and go.mod hashes must be computed separately")
	}
	// go.sum lookup must find the /go.mod line independently.
	goSumPath := writeGoSum(t, zipHash, modHash)
	ref, err := ParseGoSum(goSumPath, testModule, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if ref == nil || ref.ModHash != modHash || ref.ZipHash != zipHash {
		t.Fatalf("unexpected go.sum reference: %+v", ref)
	}
	// Unknown module version yields no source.
	ref, err = ParseGoSum(goSumPath, testModule, "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if ref != nil {
		t.Fatalf("expected nil reference for unknown version, got %+v", ref)
	}
}
