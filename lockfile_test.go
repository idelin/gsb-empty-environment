package netsec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLockJSON = `{
  "version": 2,
  "dependencies": {
    "net8.0": {
      "App.Core": {
        "type": "Direct",
        "requested": "[1.0.0, 2.0.0)",
        "resolved": "1.2.0",
        "contentHash": "AAAA",
        "dependencies": {
          "Logging": "0.9.0"
        }
      },
      "Logging": {
        "type": "Transitive",
        "resolved": "0.9.0",
        "contentHash": "BBBB",
        "dependencies": {}
      }
    }
  }
}`

func TestParseLockFileShape(t *testing.T) {
	lf, err := ParseLockFile(strings.NewReader(sampleLockJSON))
	if err != nil {
		t.Fatal(err)
	}
	if lf.Version != 2 {
		t.Fatalf("version = %d, want 2", lf.Version)
	}
	app, ok := lf.Dependencies["net8.0"]["App.Core"]
	if !ok {
		t.Fatal("missing App.Core entry")
	}
	if app.Type != TypeDirect || app.Resolved != "1.2.0" || app.ContentHash != "AAAA" {
		t.Fatalf("unexpected entry %+v", app)
	}
	if app.Dependencies["Logging"] != "0.9.0" {
		t.Fatalf("dependency map not decoded: %+v", app.Dependencies)
	}
}

func TestParseLockFileRejectsGarbage(t *testing.T) {
	if _, err := ParseLockFile(strings.NewReader("{not json")); err == nil {
		t.Fatal("malformed JSON must return an error")
	}
}

func TestEndToEndWithNupkgFiles(t *testing.T) {
	dir := t.TempDir()

	// Simulate downloaded .nupkg payloads and record their real hashes.
	writePackage := func(name, payload string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	appPath := writePackage("app.core.1.2.0.nupkg", "app-bytes")
	logPath := writePackage("logging.0.9.0.nupkg", "logging-bytes")

	appHash, err := SHA512Base64(strings.NewReader("app-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	logHash, err := SHA512Base64(strings.NewReader("logging-bytes"))
	if err != nil {
		t.Fatal(err)
	}

	lf := &LockFile{
		Version: 2,
		Dependencies: map[string]map[string]LockEntry{
			"net8.0": {
				"App.Core": entry(TypeDirect, "[1.0.0,2.0.0)", "1.2.0", appHash,
					map[string]string{"Logging": "0.9.0"}),
				"Logging": entry(TypeTransitive, "[0.9.0, )", "0.9.0", logHash, nil),
			},
		},
	}

	paths := map[string]string{
		"app.core@1.2.0": appPath,
		"logging@0.9.0":  logPath,
	}
	hashFunc := NupkgHashFunc(func(id, version string) (string, bool) {
		p, ok := paths[strings.ToLower(id)+"@"+version]
		return p, ok
	})

	if err := Verify(lf, Options{
		ActualHash:       hashFunc,
		RootDependencies: map[string][]string{"net8.0": {"App.Core"}},
	}); err != nil {
		t.Fatalf("end-to-end verification failed: %v", err)
	}

	// Tamper with the on-disk package and confirm the mismatch is caught.
	if err := os.WriteFile(appPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Verify(lf, Options{
		ActualHash:       hashFunc,
		RootDependencies: map[string][]string{"net8.0": {"App.Core"}},
	})
	if !codesOf(t, err)[ErrHashMismatch] {
		t.Fatalf("tampered package must produce %s, got %v", ErrHashMismatch, err)
	}
}

func TestVerifyNupkgHashHelper(t *testing.T) {
	good := stdHash("payload")
	if err := VerifyNupkgHash(strings.NewReader("payload"), good); err != nil {
		t.Fatalf("matching hash should pass: %v", err)
	}
	if err := VerifyNupkgHash(strings.NewReader("other"), good); err == nil {
		t.Fatal("different payload must fail")
	}
	if err := VerifyNupkgHash(strings.NewReader("payload"), urlSafeHash("payload")); err == nil {
		t.Fatal("url-safe expected hash must be rejected")
	}
}

func TestStructuralGuardrails(t *testing.T) {
	if err := Verify(nil, Options{ActualHash: hashTable{}.lookup}); err == nil {
		t.Fatal("nil lock file must fail")
	}
	lf, _, _ := validGraph()
	if err := Verify(lf, Options{}); err == nil {
		t.Fatal("missing hash resolver must fail")
	}
	empty := &LockFile{}
	if err := Verify(empty, Options{ActualHash: hashTable{}.lookup}); err == nil {
		t.Fatal("empty lock file must fail")
	}
}
