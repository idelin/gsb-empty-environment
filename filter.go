// Package netsec provides referrers-list filtering for OCI verification
// clients. A registry may return a poisoned referrers list for a subject
// (forged signatures or attestations pushed by anyone with push access).
// Before any descriptor is handed to signature verification, this package
// enforces that:
//
//   - the descriptor's artifactType is in the caller's allowlist;
//   - the manifest content digest matches the descriptor digest;
//   - the manifest's subject digest is exactly the digest being queried.
//
// The same checks are applied to descriptors discovered through the
// fallback tag scheme (sha256-<hex> index).
package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Descriptor is the subset of an OCI content descriptor relevant to
// referrer filtering.
type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// manifest is the subset of an OCI image or artifact manifest needed to
// validate a referrer.
type manifest struct {
	Subject *Descriptor `json:"subject,omitempty"`
}

// index is the subset of an OCI image index used by the fallback tag
// scheme.
type index struct {
	Manifests []Descriptor `json:"manifests"`
}

// FetchFunc retrieves the manifest bytes stored under the given digest.
// Implementations typically call GET /v2/<repo>/manifests/<digest>.
type FetchFunc func(ctx context.Context, digest string) ([]byte, error)

// ErrNoSubject is returned by FallbackTag when the subject digest is not a
// sha256 digest and therefore has no fallback tag.
var ErrNoSubject = errors.New("netsec: subject digest is not sha256")

// FallbackTag returns the tag (sha256-<hex>) under which a registry that
// lacks the referrers API stores the referrers index for subject.
func FallbackTag(subject string) (string, error) {
	alg, hexPart, ok := strings.Cut(subject, ":")
	if !ok || alg != "sha256" || len(hexPart) != sha256.Size*2 {
		return "", ErrNoSubject
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", ErrNoSubject
	}
	return "sha256-" + hexPart, nil
}

// ParseFallbackIndex decodes the index document stored under the fallback
// tag and returns the descriptors it lists. The returned descriptors must
// still be passed through FilterReferrers before use.
func ParseFallbackIndex(indexBytes []byte) ([]Descriptor, error) {
	var idx index
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return nil, fmt.Errorf("netsec: decoding fallback index: %w", err)
	}
	return idx.Manifests, nil
}

// FilterReferrers returns the subset of descs that is safe to feed into
// signature verification for the given subject digest. A descriptor is
// kept only if its artifactType is allowed, its manifest content hashes
// to the descriptor digest, and that manifest's subject is exactly
// subject. Descriptors are de-duplicated by digest; the first verified
// occurrence wins. Fetch failures and malformed entries are dropped, not
// fatal.
func FilterReferrers(ctx context.Context, subject string, descs []Descriptor, allowed map[string]bool, fetch FetchFunc) []Descriptor {
	if fetch == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(descs))
	kept := make([]Descriptor, 0, len(descs))
	for _, d := range descs {
		if !allowed[d.ArtifactType] {
			continue
		}
		if _, dup := seen[d.Digest]; dup {
			continue
		}
		seen[d.Digest] = struct{}{}
		if err := verifyManifest(ctx, subject, d, fetch); err != nil {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// verifyManifest fetches the manifest named by d.Digest and checks that
// the content digest and the embedded subject match expectations.
func verifyManifest(ctx context.Context, subject string, d Descriptor, fetch FetchFunc) error {
	if _, err := parseDigest(d.Digest); err != nil {
		return err
	}
	blob, err := fetch(ctx, d.Digest)
	if err != nil {
		return fmt.Errorf("netsec: fetching %s: %w", d.Digest, err)
	}
	sum := sha256.Sum256(blob)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != d.Digest {
		return fmt.Errorf("netsec: content digest mismatch for %s (got %s)", d.Digest, got)
	}
	var m manifest
	if err := json.Unmarshal(blob, &m); err != nil {
		return fmt.Errorf("netsec: decoding manifest %s: %w", d.Digest, err)
	}
	if m.Subject == nil || m.Subject.Digest != subject {
		return fmt.Errorf("netsec: manifest %s does not reference subject %s", d.Digest, subject)
	}
	return nil
}

// parseDigest validates a sha256 digest string.
func parseDigest(digest string) (string, error) {
	alg, hexPart, ok := strings.Cut(digest, ":")
	if !ok || alg != "sha256" || len(hexPart) != sha256.Size*2 {
		return "", fmt.Errorf("netsec: invalid digest %q", digest)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("netsec: invalid digest %q", digest)
	}
	return hexPart, nil
}
