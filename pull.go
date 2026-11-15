package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"regexp"
	"strings"
)

// digestPattern matches the OCI digest grammar: algorithm ":" encoded digest.
var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-zA-Z0-9=_-]+$`)

// fixedDigestLengths records the expected hex length of digest algorithms
// whose hash size is known, rejecting truncated digests early.
var fixedDigestLengths = map[string]int{
	"sha256": 64,
	"sha512": 128,
}

// Fetcher retrieves a blob identified by digest from one host entry.
// Implementations build the request URL (honoring OverridePath and Header)
// and return the response body. A failing fetch returns an error so that
// PullVerified can fall through to the next candidate host.
type Fetcher interface {
	Fetch(ctx context.Context, host HostEntry, digest string) (io.ReadCloser, error)
}

// FetcherFunc adapts a plain function to the Fetcher interface.
type FetcherFunc func(ctx context.Context, host HostEntry, digest string) (io.ReadCloser, error)

// Fetch implements Fetcher.
func (f FetcherFunc) Fetch(ctx context.Context, host HostEntry, digest string) (io.ReadCloser, error) {
	return f(ctx, host, digest)
}

// PullVerified pulls a digest-referenced blob through the configured hosts.
// It enforces two guarantees:
//
//   - Only hosts with the pull capability are contacted; a resolve-only
//     mirror is skipped, so it can never serve a pull.
//   - Every fetched body is streamed through a hash and compared with the
//     requested digest before the bytes are returned. On mismatch the body
//     is closed and discarded in full and the next host is tried; the
//     untrusted bytes never leave this function.
func PullVerified(ctx context.Context, cfg *Config, policy *Policy, digest string, fetcher Fetcher) ([]byte, error) {
	if err := Validate(cfg, policy); err != nil {
		return nil, err
	}
	if policy == nil {
		policy = DefaultPolicy()
	}
	if fetcher == nil {
		return nil, errors.New("nil fetcher")
	}

	hasher, err := newDigestHasher(digest, policy.digestAlgorithms())
	if err != nil {
		return nil, err
	}

	candidates, err := pullHosts(cfg)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrNoPullHost
	}

	var attempts []error
	for _, host := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		body, fetchErr := fetcher.Fetch(ctx, host, digest)
		if fetchErr != nil {
			attempts = append(attempts, fmt.Errorf("fetch %q: %w", host.URL, fetchErr))
			continue
		}

		// Tee into the hasher while buffering; the buffer is returned only
		// after the digest has been verified.
		hasher.Reset()
		content, copyErr := io.ReadAll(io.TeeReader(body, hasher))
		_ = body.Close()
		if copyErr != nil {
			attempts = append(attempts, fmt.Errorf("read %q: %w", host.URL, copyErr))
			continue
		}

		if hex.EncodeToString(hasher.Sum(nil)) != digestHex(digest) {
			// Discard the downloaded content entirely: it must never be
			// reused or returned before falling through to the next host.
			content = nil
			attempts = append(attempts, configError(host.URL, "pull.digest",
				fmt.Errorf("%w: expected %s", ErrDigestMismatch, digest)))
			continue
		}

		return content, nil
	}

	return nil, errors.Join(attempts...)
}

// newDigestHasher validates the digest syntax and algorithm policy and
// returns a fresh hash.Hash for the requested algorithm.
func newDigestHasher(digest string, allowedAlgorithms []string) (hash.Hash, error) {
	if !digestPattern.MatchString(digest) {
		return nil, fmt.Errorf("malformed digest %q", digest)
	}
	algorithm := digest[:strings.IndexByte(digest, ':')]

	allowed := false
	for _, name := range allowedAlgorithms {
		if name == algorithm {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: %s", ErrDigestNotAllowed, algorithm)
	}

	if wantLen, known := fixedDigestLengths[algorithm]; known && len(digestHex(digest)) != wantLen {
		return nil, fmt.Errorf("malformed digest %q: wrong length for %s", digest, algorithm)
	}

	switch algorithm {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: %s has no registered hasher", ErrDigestNotAllowed, algorithm)
	}
}

func digestHex(digest string) string {
	return digest[strings.IndexByte(digest, ':')+1:]
}
