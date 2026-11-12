package composer

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var (
	distURL     = "https://dist.example.com/monolog/monolog/abc123.zip"
	packageBody = []byte("fake zip payload for monolog")
)

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// composerJSON returns a valid composer.json; mutators can tamper with it.
func composerJSON(mutators ...func(map[string]any)) []byte {
	doc := map[string]any{
		"name":    "acme/app",
		"version": "1.0.0",
		"require": map[string]any{
			"php":             ">=8.1",
			"monolog/monolog": "^3.0",
		},
		"repositories": []any{
			map[string]any{"type": "composer", "url": "https://repo.example.com"},
		},
	}
	for _, m := range mutators {
		m(doc)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return out
}

// lockFor builds a lock matching the given composer.json; mutators can tamper
// with the package entry.
func lockFor(t *testing.T, composerJSON []byte, mutators ...func(map[string]any)) []byte {
	t.Helper()
	hash, err := ContentHash(composerJSON)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	pkg := map[string]any{
		"name": "monolog/monolog",
		"dist": map[string]any{
			"type":      "zip",
			"url":       distURL,
			"reference": "abc123",
			"shasum":    sha1Hex(packageBody),
		},
		"source": map[string]any{
			"type":      "git",
			"url":       "https://github.com/monolog/monolog.git",
			"reference": "abc123",
		},
	}
	for _, m := range mutators {
		m(pkg)
	}
	lock := map[string]any{
		"content-hash": hash,
		"packages":     []any{pkg},
	}
	out, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	return out
}

func newTestValidator() *Validator {
	fetcher := func(url string) ([]byte, error) {
		if url == distURL {
			return packageBody, nil
		}
		return nil, fmt.Errorf("unexpected fetch %s", url)
	}
	return NewValidator([]string{"dist.example.com", "repo.example.com", "github.com"}, WithFetcher(fetcher))
}

func setDist(pkg map[string]any, key string, value any) {
	pkg["dist"].(map[string]any)[key] = value
}

func TestValidLockPasses(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer)
	if err := newTestValidator().Validate(composer, lock); err != nil {
		t.Fatalf("expected valid lock, got %v", err)
	}
}

func TestRequireModifiedWithoutLockUpdate(t *testing.T) {
	lock := lockFor(t, composerJSON())
	tampered := composerJSON(func(doc map[string]any) {
		doc["require"].(map[string]any)["monolog/monolog"] = "^9.9"
	})
	err := newTestValidator().Validate(tampered, lock)
	if err == nil || !strings.Contains(err.Error(), "content-hash") {
		t.Fatalf("expected content-hash mismatch, got %v", err)
	}
}

func TestDistReferenceMismatch(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer, func(pkg map[string]any) {
		setDist(pkg, "reference", "evil999")
	})
	err := newTestValidator().Validate(composer, lock)
	if err == nil || !strings.Contains(err.Error(), "dist.reference") {
		t.Fatalf("expected reference mismatch, got %v", err)
	}
}

func TestDistURLHostNotWhitelisted(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer, func(pkg map[string]any) {
		setDist(pkg, "url", "https://attacker.example.net/abc123.zip")
	})
	err := newTestValidator().Validate(composer, lock)
	if err == nil || !strings.Contains(err.Error(), "whitelist") {
		t.Fatalf("expected whitelist rejection, got %v", err)
	}
}

func TestDistShasumMismatch(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer, func(pkg map[string]any) {
		setDist(pkg, "shasum", sha1Hex([]byte("tampered payload")))
	})
	err := newTestValidator().Validate(composer, lock)
	if err == nil || !strings.Contains(err.Error(), "shasum") {
		t.Fatalf("expected shasum mismatch, got %v", err)
	}
}

func TestDistTypeNotZip(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer, func(pkg map[string]any) {
		setDist(pkg, "type", "tar")
	})
	err := newTestValidator().Validate(composer, lock)
	if err == nil || !strings.Contains(err.Error(), "dist.type") {
		t.Fatalf("expected dist.type rejection, got %v", err)
	}
}

func TestRepositoryPathOrVCSHostNotWhitelisted(t *testing.T) {
	cases := map[string]map[string]any{
		"path": map[string]any{"type": "path", "url": "../local-lib"},
		"vcs":  map[string]any{"type": "vcs", "url": "https://git.attacker.example.net/repo.git"},
	}
	for typ, repo := range cases {
		t.Run(typ, func(t *testing.T) {
			composer := composerJSON(func(doc map[string]any) {
				doc["repositories"] = []any{repo}
			})
			lock := lockFor(t, composer)
			err := newTestValidator().Validate(composer, lock)
			if err == nil || !strings.Contains(err.Error(), "repository") {
				t.Fatalf("expected repository rejection, got %v", err)
			}
		})
	}
}

func TestRepositoryVCSWhitelistedHostPasses(t *testing.T) {
	composer := composerJSON(func(doc map[string]any) {
		doc["repositories"] = []any{
			map[string]any{"type": "vcs", "url": "https://github.com/acme/lib.git"},
		}
	})
	lock := lockFor(t, composer)
	if err := newTestValidator().Validate(composer, lock); err != nil {
		t.Fatalf("expected whitelisted vcs repo to pass, got %v", err)
	}
}

func TestEmptyShasumSkipsFetch(t *testing.T) {
	composer := composerJSON()
	lock := lockFor(t, composer, func(pkg map[string]any) {
		setDist(pkg, "shasum", "")
	})
	v := NewValidator([]string{"dist.example.com"}, WithFetcher(func(url string) ([]byte, error) {
		t.Fatalf("fetcher must not be called when shasum is empty")
		return nil, nil
	}))
	if err := v.Validate(composer, lock); err != nil {
		t.Fatalf("expected empty shasum to pass, got %v", err)
	}
}

func TestContentHashStableAcrossKeyOrder(t *testing.T) {
	a := []byte(`{"name":"acme/app","version":"1.0.0","require":{"php":">=8.1"}}`)
	b := []byte(`{"require":{"php":">=8.1"},"version":"1.0.0","name":"acme/app","description":"ignored"}`)
	ha, err := ContentHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := ContentHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash should be key-order independent and ignore extra fields: %s != %s", ha, hb)
	}
}
