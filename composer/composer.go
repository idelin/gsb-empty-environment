// Package composer validates composer.lock files against their composer.json
// and a caller-supplied host whitelist, for use in build gateways.
package composer

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hashKeys is the agreed subset of composer.json fields covered by the
// content-hash.
var hashKeys = []string{
	"name",
	"version",
	"require",
	"require-dev",
	"conflict",
	"replace",
	"provide",
	"minimum-stability",
	"prefer-stable",
	"repositories",
}

// Fetcher retrieves the bytes of a dist package. It is injectable so callers
// can route downloads through their own cache or sandbox.
type Fetcher func(url string) ([]byte, error)

// Validator checks composer.lock files against a host whitelist.
type Validator struct {
	allowed map[string]struct{}
	fetch   Fetcher
}

// Option customizes a Validator.
type Option func(*Validator)

// WithFetcher overrides the default HTTP fetcher used for shasum checks.
func WithFetcher(f Fetcher) Option {
	return func(v *Validator) { v.fetch = f }
}

// NewValidator builds a Validator that only trusts dist and repository hosts
// listed in allowedHosts (case-insensitive, matched by hostname).
func NewValidator(allowedHosts []string, opts ...Option) *Validator {
	v := &Validator{allowed: make(map[string]struct{}, len(allowedHosts))}
	for _, h := range allowedHosts {
		v.allowed[strings.ToLower(h)] = struct{}{}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	v.fetch = func(rawurl string) ([]byte, error) {
		resp, err := client.Get(rawurl)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", rawurl, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// ContentHash computes the agreed content-hash for a composer.json document:
// MD5 of the compact JSON encoding of the hashKeys subset, sorted by key.
func ContentHash(composerJSON []byte) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(composerJSON, &doc); err != nil {
		return "", fmt.Errorf("parse composer.json: %w", err)
	}
	subset := make(map[string]json.RawMessage, len(hashKeys))
	for _, k := range hashKeys {
		if raw, ok := doc[k]; ok {
			var compacted bytes.Buffer
			if err := json.Compact(&compacted, raw); err != nil {
				return "", fmt.Errorf("compact field %q: %w", k, err)
			}
			subset[k] = json.RawMessage(compacted.Bytes())
		}
	}
	// encoding/json marshals maps with sorted keys and no whitespace.
	compact, err := json.Marshal(subset)
	if err != nil {
		return "", fmt.Errorf("encode hash subset: %w", err)
	}
	sum := md5.Sum(compact)
	return hex.EncodeToString(sum[:]), nil
}

type lockFile struct {
	ContentHash string `json:"content-hash"`
	Packages    []struct {
		Name   string `json:"name"`
		Dist   dist   `json:"dist"`
		Source source `json:"source"`
	} `json:"packages"`
}

type dist struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
	Shasum    string `json:"shasum"`
}

type source struct {
	Reference string `json:"reference"`
}

type repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Validate checks the lock file against composer.json and the whitelist.
// It returns the first violation found.
func (v *Validator) Validate(composerJSON, lockJSON []byte) error {
	hash, err := ContentHash(composerJSON)
	if err != nil {
		return err
	}

	var lock lockFile
	if err := json.Unmarshal(lockJSON, &lock); err != nil {
		return fmt.Errorf("parse composer.lock: %w", err)
	}
	if lock.ContentHash == "" {
		return fmt.Errorf("composer.lock: missing content-hash")
	}
	if lock.ContentHash != hash {
		return fmt.Errorf("content-hash mismatch: composer.json changed without updating the lock (lock %s, computed %s)", lock.ContentHash, hash)
	}

	if err := v.checkRepositories(composerJSON); err != nil {
		return err
	}

	for _, pkg := range lock.Packages {
		if err := v.checkPackage(pkg.Name, pkg.Dist, pkg.Source); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) checkRepositories(composerJSON []byte) error {
	var doc struct {
		Repositories []repository `json:"repositories"`
	}
	if err := json.Unmarshal(composerJSON, &doc); err != nil {
		return fmt.Errorf("parse composer.json repositories: %w", err)
	}
	for _, repo := range doc.Repositories {
		if repo.Type != "path" && repo.Type != "vcs" {
			continue
		}
		if !v.hostAllowed(repo.URL) {
			return fmt.Errorf("repository %q (type %s): host not in whitelist", repo.URL, repo.Type)
		}
	}
	return nil
}

func (v *Validator) checkPackage(name string, d dist, s source) error {
	if d.Type != "zip" {
		return fmt.Errorf("package %s: dist.type %q is not zip", name, d.Type)
	}
	if !v.hostAllowed(d.URL) {
		return fmt.Errorf("package %s: dist.url host not in whitelist: %s", name, d.URL)
	}
	if d.Reference != s.Reference {
		return fmt.Errorf("package %s: dist.reference %q does not match source.reference %q", name, d.Reference, s.Reference)
	}
	if d.Shasum != "" {
		body, err := v.fetch(d.URL)
		if err != nil {
			return fmt.Errorf("package %s: fetch dist for shasum check: %w", name, err)
		}
		sum := sha1.Sum(body)
		if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, d.Shasum) {
			return fmt.Errorf("package %s: dist.shasum %s does not match package SHA-1 %s", name, d.Shasum, got)
		}
	}
	return nil
}

func (v *Validator) hostAllowed(rawurl string) bool {
	u, err := url.Parse(rawurl)
	if err != nil {
		return false
	}
	_, ok := v.allowed[strings.ToLower(u.Hostname())]
	return ok
}
