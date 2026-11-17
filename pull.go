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
	"os"
	"path/filepath"
	"strings"
)

// ErrDigestMismatch means the bytes received from a host did not match the
// digest named by the reference. Already fetched content is discarded before
// the next host is tried.
var ErrDigestMismatch = errors.New("content digest mismatch")

// ErrNoPullHost means every eligible host refused or failed to serve a blob.
var ErrNoPullHost = errors.New("no host available to pull the content")

// ErrBadDigest means the digest portion of a reference is malformed.
var ErrBadDigest = errors.New("invalid digest")

// Digest is an algorithm/hex-encoded content digest, e.g. sha256:abc....
type Digest struct {
	Algorithm string
	Encoded   string
}

// ParseDigest parses "algo:hex" digests. sha256 and sha512 are supported.
func ParseDigest(value string) (Digest, error) {
	algo, encoded, ok := strings.Cut(value, ":")
	if !ok || encoded == "" {
		return Digest{}, fmt.Errorf("%w: %q", ErrBadDigest, value)
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return Digest{}, fmt.Errorf("%w: %q: %v", ErrBadDigest, value, err)
	}
	switch algo {
	case "sha256":
		if len(decoded) != sha256.Size {
			return Digest{}, fmt.Errorf("%w: %q: wrong sha256 length", ErrBadDigest, value)
		}
	case "sha512":
		if len(decoded) != sha512.Size {
			return Digest{}, fmt.Errorf("%w: %q: wrong sha512 length", ErrBadDigest, value)
		}
	default:
		return Digest{}, fmt.Errorf("%w: %q: unsupported algorithm %q", ErrBadDigest, value, algo)
	}
	return Digest{Algorithm: algo, Encoded: encoded}, nil
}

func (d Digest) String() string { return d.Algorithm + ":" + d.Encoded }

func (d Digest) newHasher() (hash.Hash, error) {
	switch d.Algorithm {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: unsupported algorithm %q", ErrBadDigest, d.Algorithm)
	}
}

// Reference identifies content in a registry. A tag reference only pins a
// name; a digest reference additionally pins the expected content.
type Reference struct {
	Repository string
	Tag        string
	Digest     *Digest
}

// ParseReference accepts the familiar repository[:tag][@digest] shape.
func ParseReference(ref string) (Reference, error) {
	if ref == "" {
		return Reference{}, errors.New("empty reference")
	}
	out := Reference{}
	remainder := ref
	if at := strings.Index(remainder, "@"); at >= 0 {
		digest, err := ParseDigest(remainder[at+1:])
		if err != nil {
			return Reference{}, err
		}
		out.Digest = &digest
		remainder = remainder[:at]
	}
	if remainder != "" {
		name, tag, hasTag := strings.Cut(remainder, ":")
		out.Repository = name
		if hasTag {
			if tag == "" || strings.Contains(tag, ":") {
				return Reference{}, fmt.Errorf("invalid reference %q", ref)
			}
			out.Tag = tag
		}
	}
	if out.Repository == "" {
		return Reference{}, fmt.Errorf("invalid reference %q: missing repository", ref)
	}
	return out, nil
}

// EndpointClient abstracts the containerd registry transport. Implementations
// resolve references through an endpoint and stream blobs to the supplied
// writer. The host entry's headers are applied by the implementation; they
// have already been admission-checked by Validate.
type EndpointClient interface {
	// Resolve asks the host where the named content lives. The returned
	// digest is authoritative when the reference only carries a tag.
	Resolve(ctx context.Context, host Host, ref Reference) (Digest, error)
	// Fetch streams the blob identified by digest from the host.
	Fetch(ctx context.Context, host Host, digest Digest, w io.Writer) error
}

// Puller performs registry pulls with admission-aware host selection and
// digest enforcement.
type Puller struct {
	Client EndpointClient
	Policy Policy
	// Dir receives the atomically renamed content file. It defaults to the
	// process temporary directory.
	Dir string
}

// Pull fetches ref from the configured hosts and returns the path of the
// verified content file plus its digest.
//
// Hosts are tried in hosts.toml order. Hosts without the pull capability
// (including policy-designated resolve-only mirrors) are skipped. When a
// digest-pinned reference yields content with a different digest, the
// partially written content is discarded before moving on to the next host.
func (p *Puller) Pull(ctx context.Context, cfg *Config, ref string) (path string, digest Digest, err error) {
	if p.Client == nil {
		return "", Digest{}, errors.New("puller: nil EndpointClient")
	}
	if verr := Validate(cfg, p.Policy); verr != nil {
		return "", Digest{}, verr
	}
	parsed, err := ParseReference(ref)
	if err != nil {
		return "", Digest{}, err
	}

	resolveOnly := make(map[string]bool, len(p.Policy.ResolveOnlyHosts))
	for _, value := range p.Policy.ResolveOnlyHosts {
		key, keyErr := canonicalKey(value)
		if keyErr != nil {
			return "", Digest{}, keyErr
		}
		resolveOnly[key] = true
	}

	var failures []error
	for i := range cfg.Hosts {
		host := &cfg.Hosts[i]
		ep, parseErr := parseEndpoint(host.Host)
		if parseErr != nil {
			failures = append(failures, &ValidationError{Host: host.Host, Err: parseErr})
			continue
		}
		if !hasCapability(host.Capabilities, CapPull) || resolveOnly[ep.host] {
			continue
		}

		path, digest, err = p.pullFromHost(ctx, host, ep, parsed)
		switch {
		case err == nil:
			return path, digest, nil
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return "", Digest{}, err
		default:
			failures = append(failures, &ValidationError{Host: host.Host, Err: err})
		}
	}

	if len(failures) == 0 {
		return "", Digest{}, fmt.Errorf("%w: %s", ErrNoPullHost, ref)
	}
	return "", Digest{}, fmt.Errorf("%w: %s: %w", ErrNoPullHost, ref, errors.Join(failures...))
}

func (p *Puller) pullFromHost(ctx context.Context, host *Host, ep *endpoint, ref Reference) (string, Digest, error) {
	expected := ref.Digest
	if expected == nil {
		resolved, err := p.Client.Resolve(ctx, *host, ref)
		if err != nil {
			return "", Digest{}, fmt.Errorf("resolve: %w", err)
		}
		expected = &resolved
	}

	hasher, err := expected.newHasher()
	if err != nil {
		return "", Digest{}, err
	}

	dir := p.Dir
	if dir == "" {
		dir = os.TempDir()
	}
	temp, err := os.CreateTemp(dir, ".netsec-pull-*")
	if err != nil {
		return "", Digest{}, fmt.Errorf("create temp file: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() {
		temp.Close()
		os.Remove(tempName)
	}

	if err := p.Client.Fetch(ctx, *host, *expected, io.MultiWriter(temp, hasher)); err != nil {
		cleanup()
		return "", Digest{}, fmt.Errorf("fetch: %w", err)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expected.Encoded {
		// Discard the untrusted bytes before another host is contacted.
		cleanup()
		return "", Digest{}, fmt.Errorf("%w: expected %s, got %s:%s from %s",
			ErrDigestMismatch, expected, expected.Algorithm, actual, ep.host)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return "", Digest{}, fmt.Errorf("close content file: %w", err)
	}

	finalName := filepath.Join(dir, strings.ReplaceAll(expected.String(), ":", "-"))
	if err := os.Rename(tempName, finalName); err != nil {
		os.Remove(tempName)
		return "", Digest{}, fmt.Errorf("commit content file: %w", err)
	}
	return finalName, *expected, nil
}
