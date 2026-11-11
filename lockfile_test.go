package depseal

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseLockfileValid(t *testing.T) {
	lock := mustParse(t, validLockfileJSON(t, "http://127.0.0.1:9000"))

	pkg, ok := lock.Lookup("lib/internal", "1.2.0")
	if !ok {
		t.Fatal("locked package not found")
	}
	if pkg.Source != "private" {
		t.Fatalf("source = %q, want private", pkg.Source)
	}

	leaf, ok := lock.Lookup("lib/leaf", "0.9.1")
	if !ok {
		t.Fatal("leaf package not found")
	}
	path, err := lock.DependencyPath(leaf)
	if err != nil {
		t.Fatalf("dependency path: %v", err)
	}
	got := make([]string, len(path))
	for i, p := range path {
		got[i] = p.Identity()
	}
	want := []string{"lib/leaf@0.9.1", "lib/internal@1.2.0", "app/root@3.0.0"}
	if strings.Join(got, "->") != strings.Join(want, "->") {
		t.Fatalf("dependency path = %v, want %v", got, want)
	}
}

func TestParseLockfileRejects(t *testing.T) {
	validDigest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	cases := map[string]string{
		"wrong version":         `{"version":2,"sources":{},"packages":[]}`,
		"unknown source":        `{"version":1,"sources":{"private":{"id":"private","base_url":"https://registry.example"}},"packages":[{"name":"a","version":"1","source":"public","digest":"` + validDigest + `"}]}`,
		"malformed digest":      `{"version":1,"sources":{"private":{"id":"private","base_url":"https://registry.example"}},"packages":[{"name":"a","version":"1","source":"private","digest":"md5:abc"}]}`,
		"duplicate package":     `{"version":1,"sources":{"private":{"id":"private","base_url":"https://registry.example"}},"packages":[{"name":"a","version":"1","source":"private","digest":"` + validDigest + `"},{"name":"a","version":"1","source":"private","digest":"` + validDigest + `"}]}`,
		"dangling parent":       `{"version":1,"sources":{"private":{"id":"private","base_url":"https://registry.example"}},"packages":[{"name":"a","version":"1","source":"private","digest":"` + validDigest + `","parent":"ghost@9"}]}`,
		"http non-loopback":     `{"version":1,"sources":{"private":{"id":"private","base_url":"http://registry.example"}},"packages":[{"name":"a","version":"1","source":"private","digest":"` + validDigest + `"}]}`,
		"mismatching source id": `{"version":1,"sources":{"private":{"id":"other","base_url":"https://registry.example"}},"packages":[{"name":"a","version":"1","source":"private","digest":"` + validDigest + `"}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseLockfile(strings.NewReader(doc)); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func mustParse(t *testing.T, doc string) *Lockfile {
	t.Helper()
	lock, err := ParseLockfile(bytes.NewReader([]byte(doc)))
	if err != nil {
		t.Fatalf("parse lock file: %v", err)
	}
	return lock
}
