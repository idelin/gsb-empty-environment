package netsec

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// Verifier validates podspecs served by a CocoaPods specs mirror against a
// trusted Podfile.lock. The zero value is not usable; construct one with
// NewVerifier.
type Verifier struct {
	lock *Lockfile
}

// NewVerifier builds a Verifier bound to the parsed Podfile.lock.
func NewVerifier(lock *Lockfile) *Verifier {
	return &Verifier{lock: lock}
}

// Verify checks one podspec before it is served from the mirror.
//
// repoKey identifies the specs repository the bytes were fetched from. It is
// matched against the keys under SPEC REPOS in Podfile.lock ("trunk", a git
// URL, ...), so a same-name pod published in another source is rejected
// regardless of source ordering in the Podfile.
//
// relPath is the path of the podspec inside that repository, e.g.
// Specs/d/a/2/Alamofire/5.6.4/Alamofire.podspec.json.
func (v *Verifier) Verify(repoKey, relPath string, data []byte) (*Podspec, error) {
	if v == nil || v.lock == nil {
		return nil, fmt.Errorf("%w: verifier has no Podfile.lock", ErrInvalidPodspec)
	}

	spec, err := ParsePodspec(data)
	if err != nil {
		return nil, err
	}
	if !safePathPart(spec.Name) || !safePathPart(spec.Version) {
		return nil, fmt.Errorf("%w: unsafe name or version", ErrInvalidPodspec)
	}

	// 1. The bytes must sit at the MD5-sharded path derived from name/version.
	wantPath := SpecPath(spec.Name, spec.Version)
	if cleanRelPath(relPath) != wantPath {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrSpecPathMismatch, relPath, wantPath)
	}

	// 2. The SHA-1 of the exact bytes must match SPEC CHECKSUMS. Any edit to
	// the source changes the digest and fails here.
	wantChecksum, ok := v.lock.ChecksumForPod(spec.Name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrChecksumMissing, spec.Name)
	}
	actualChecksum := sha1.Sum(data)
	if !equalFoldHex(wantChecksum, hex.EncodeToString(actualChecksum[:])) {
		return nil, fmt.Errorf("%w: %s", ErrChecksumMismatch, spec.Name)
	}

	// 3. The repository actually used must be the one recorded under
	// SPEC REPOS, not another source hosting a same-name pod.
	lockRepo, ok := v.lock.RepoForPod(spec.Name)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not listed under SPEC REPOS", ErrSpecRepoMismatch, spec.Name)
	}
	if normalizeRepoKey(repoKey) != lockRepo {
		return nil, fmt.Errorf("%w: %s resolved from %q, lock requires %q",
			ErrSpecRepoMismatch, spec.Name, repoKey, lockRepo)
	}

	// 4. Source policy: no http archives, git must be commit-pinned.
	if err := validateSource(spec.Source); err != nil {
		return nil, err
	}

	return spec, nil
}

// equalFoldHex compares two hex strings case-insensitively.
func equalFoldHex(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'F' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'F' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
