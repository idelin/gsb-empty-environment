package netsec

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestVerifyFS(t *testing.T) {
	pod := podFixture{
		name:    "Alamofire",
		version: "5.6.4",
		repo:    "trunk",
		source:  gitSource("https://github.com/Alamofire/Alamofire.git", fullCommit),
	}
	data := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})
	relPath := SpecPath(pod.name, pod.version)

	fsys := fstest.MapFS{
		relPath: &fstest.MapFile{Data: data},
	}
	if _, err := NewVerifier(lock).VerifyFS(fsys, "trunk", relPath); err != nil {
		t.Fatalf("VerifyFS: %v", err)
	}

	if _, err := NewVerifier(lock).VerifyFS(fsys, "trunk", "Specs/missing.json"); !errors.Is(err, ErrInvalidPodspec) {
		t.Fatalf("missing file: want ErrInvalidPodspec, got %v", err)
	}
}

func TestVerifyRejectsInvalidPodspec(t *testing.T) {
	lock, err := ParseLockfile(strings.NewReader(""))
	if err != nil {
		t.Fatalf("empty lock parse: %v", err)
	}
	verifier := NewVerifier(lock)

	cases := map[string][]byte{
		"not json":     []byte("{not json"),
		"missing name": []byte(`{"version":"1.0","source":{"git":"https://x/y.git"}}`),
		"no source":    []byte(`{"name":"X","version":"1.0"}`),
	}
	for name, data := range cases {
		if _, err := verifier.Verify("trunk", "Specs/x/y/z/X/1.0/X.podspec.json", data); !errors.Is(err, ErrInvalidPodspec) {
			t.Fatalf("%s: want ErrInvalidPodspec, got %v", name, err)
		}
	}
}

func TestVerifyRejectsUnsupportedAndUnsafeSpecs(t *testing.T) {
	pod := podFixture{
		name:    "Lib",
		version: "1.0",
		repo:    "trunk",
		source:  map[string]any{"svn": "https://svn.example.com/repo"},
	}
	data := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})

	_, err := NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), data)
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("svn source: want ErrUnsupportedSource, got %v", err)
	}

	// Names containing path separators or ".." cannot define a safe shard path.
	sneaky := mustPodspecJSON(t, "../evil", "1.0",
		gitSource("https://github.com/acme/evil.git", fullCommit))
	if _, err := NewVerifier(lock).Verify("trunk", "Specs/x/y/z/evil/1.0/evil.podspec.json", sneaky); !errors.Is(err, ErrInvalidPodspec) {
		t.Fatalf("traversal name: want ErrInvalidPodspec, got %v", err)
	}
}
