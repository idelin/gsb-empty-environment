package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Digest is an algorithm-prefixed content digest, e.g.
// "sha256:abcd...".
type Digest string

var supportedAlgorithms = map[string]func() hash.Hash{
	"sha256": sha256.New,
	"sha384": sha512.New384,
	"sha512": sha512.New,
}

// ParseDigest validates the digest's algorithm and encoded length.
func ParseDigest(value string) (Digest, error) {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm == "" || encoded == "" {
		return "", fmt.Errorf("netsec: malformed digest %q", value)
	}
	newHash, ok := supportedAlgorithms[algorithm]
	if !ok {
		return "", fmt.Errorf("netsec: unsupported digest algorithm %q", algorithm)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return "", fmt.Errorf("netsec: malformed digest encoding %q", value)
	}
	if len(encoded) != hex.EncodedLen(newHash().Size()) {
		return "", fmt.Errorf("netsec: malformed digest length %q", value)
	}
	return Digest(value), nil
}

// String returns the original digest string.
func (d Digest) String() string { return string(d) }

// Algorithm returns the digest algorithm component.
func (d Digest) Algorithm() string {
	algorithm, _, _ := strings.Cut(string(d), ":")
	return algorithm
}

// Encoded returns the hex-encoded checksum component.
func (d Digest) Encoded() string {
	_, encoded, _ := strings.Cut(string(d), ":")
	return encoded
}

func (d Digest) hasher() (hash.Hash, error) {
	newHash, ok := supportedAlgorithms[d.Algorithm()]
	if !ok {
		return nil, fmt.Errorf("netsec: unsupported digest algorithm %q", d.Algorithm())
	}
	return newHash(), nil
}

// Content is an in-progress fetch from a single host. Read returns the
// response body while its hash is accumulated. On digest mismatch the
// caller must invoke Discard (before Close) so the host's partial
// download is thrown away rather than committed.
type Content interface {
	io.ReadCloser
	// Discard releases and drops any already downloaded bytes,
	// including buffered/spooled content, without committing it.
	Discard() error
}

// Puller fetches digest-referenced content from one configured host.
type Puller interface {
	// Fetch opens the content identified by digest on host. A nil
	// error must be accompanied by a non-nil Content.
	Fetch(ctx context.Context, host HostEntry, digest Digest) (Content, error)
}

// Committer stores content that passed digest verification.
type Committer interface {
	// Commit persists payload as digest. payload is exhausted after
	// Commit returns.
	Commit(ctx context.Context, digest Digest, payload io.Reader) error
}

// PullByDigest pulls digest-referenced content through the hosts in
// config order, switching to the next host whenever a host errors or
// serves content whose digest does not match. Mismatched content is
// discarded via Content.Discard before the next host is tried, so bad
// bytes are never committed and never reused.
func PullByDigest(ctx context.Context, cfg *Config, policy Policy, digest string, puller Puller, committer Committer) error {
	if puller == nil {
		return errors.New("netsec: nil puller")
	}
	if committer == nil {
		return errors.New("netsec: nil committer")
	}

	want, err := ParseDigest(digest)
	if err != nil {
		return err
	}

	hosts, err := HostsForPull(cfg, policy)
	if err != nil {
		return err
	}

	var failures []error
	for _, host := range hosts {
		if err := ctx.Err(); err != nil {
			return err
		}

		content, fetchErr := puller.Fetch(ctx, host, want)
		if fetchErr != nil {
			failures = append(failures, &HostError{Host: host.Host, Err: fetchErr})
			continue
		}

		payload, verifyErr := spoolAndVerify(content, want)
		if verifyErr != nil {
			// Discard before moving on so the tampered partial
			// download cannot be committed or reused.
			discardErr := content.Discard()
			closeErr := content.Close()
			failures = append(failures, &HostError{
				Host: host.Host,
				Err:  errors.Join(verifyErr, discardErr, closeErr),
			})
			continue
		}

		commitErr := committer.Commit(ctx, want, bytes.NewReader(payload))
		if closeErr := content.Close(); closeErr != nil {
			failures = append(failures, &HostError{Host: host.Host, Err: closeErr})
			continue
		}
		if commitErr != nil {
			return commitErr
		}
		return nil
	}

	return errors.Join(append([]error{errors.New("netsec: content could not be pulled from any host")}, failures...)...)
}

// spoolAndVerify fully reads content into memory while hashing it, then
// compares the hash with want and returns the buffered payload. On
// mismatch the buffered bytes are dropped and ErrDigestMismatch is
// returned; the caller remains responsible for Discard/Close.
func spoolAndVerify(content Content, want Digest) ([]byte, error) {
	hasher, err := want.hasher()
	if err != nil {
		return nil, err
	}

	var spool bytes.Buffer
	if _, err := io.Copy(io.MultiWriter(&spool, hasher), content); err != nil {
		return nil, err
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != want.Encoded() {
		spool.Reset()
		return nil, fmt.Errorf("%w: expected %s, got %s:%s", ErrDigestMismatch, want, want.Algorithm(), got)
	}
	return spool.Bytes(), nil
}
