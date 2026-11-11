// Package netsec provides a scoped network capability for build hosts that may
// only fetch a fixed, pre-authorized set of dependencies.
//
// A Capability is bound to three things and can never exceed them:
//   - a SourceSet of URL prefixes the build was authorized to contact;
//   - the expected SHA-256 digest of every piece of content that may be fetched;
//   - a maximum number of bytes received in total across all requests,
//     including every failed attempt and every redirect hop.
//
// Retries share the single byte budget, so a flaky download cannot turn the
// capability into an unrestricted network grant. Redirects are checked against
// the same SourceSet. Fetched files are staged and only published once every
// target has been verified against its expected digest.
package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sentinel errors reported by the capability; inspect with errors.Is.
var (
	// ErrOriginNotAllowed means a request or redirect target is outside the
	// configured SourceSet. It is never retried.
	ErrOriginNotAllowed = errors.New("netsec: url is not in the allowed source set")

	// ErrBudgetExceeded means the shared maximum total received byte budget is
	// consumed. It is never retried and aborts the whole Fetch call.
	ErrBudgetExceeded = errors.New("netsec: maximum total received bytes exceeded")

	// ErrDigestMismatch means received content does not match the expected
	// digest. It is never retried.
	ErrDigestMismatch = errors.New("netsec: content digest mismatch")

	// ErrBadDigest means a Target carries a malformed expected digest.
	ErrBadDigest = errors.New("netsec: invalid expected digest")

	// ErrBadTarget means a Target carries an invalid output name.
	ErrBadTarget = errors.New("netsec: invalid fetch target")
)

const defaultMaxAttempts = 4

// budget counts every byte read from any response body. It is shared by all
// targets and all attempts, so retries cannot reset or extend it.
type budget struct {
	mu    sync.Mutex
	limit int64
	used  int64
}

// consume charges n received bytes against the budget. Once the limit is
// reached the counter saturates at the limit and ErrBudgetExceeded is
// returned for every further byte.
func (b *budget) consume(n int) error {
	if n <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used += int64(n)
	if b.used > b.limit {
		b.used = b.limit
		return ErrBudgetExceeded
	}
	return nil
}

func (b *budget) usedBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *budget) remaining() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit - b.used
}

// source is one normalized allow-list entry: scheme, lower-cased host and an
// exact path or a directory prefix (a path ending in "/").
type source struct {
	scheme string
	host   string
	path   string
}

// SourceSet is the closed set of origins a Capability is allowed to contact.
// Query strings are intentionally not part of a source: authorization is
// scoped to scheme, host and path only.
type SourceSet struct {
	sources []source
}

// NewSourceSet parses rawURLs into a SourceSet. Each entry must be an absolute
// http(s) URL without userinfo, query or fragment. A path ending in "/" acts
// as a directory prefix; any other path matches exactly.
func NewSourceSet(rawURLs ...string) (*SourceSet, error) {
	set := &SourceSet{}
	for _, raw := range rawURLs {
		if err := set.Add(raw); err != nil {
			return nil, err
		}
	}
	return set, nil
}

// Add registers one additional allowed source.
func (s *SourceSet) Add(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrOriginNotAllowed, rawURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: %q: only http(s) sources are allowed", ErrOriginNotAllowed, rawURL)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf(
			"%w: %q: source must be an absolute http(s) url without userinfo, query or fragment",
			ErrOriginNotAllowed, rawURL)
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	s.sources = append(s.sources, source{
		scheme: scheme,
		host:   strings.ToLower(u.Hostname()),
		path:   path,
	})
	return nil
}

// allow reports whether u falls inside the source set.
func (s *SourceSet) allow(u *url.URL) error {
	if u == nil || u.Host == "" || u.User != nil {
		return ErrOriginNotAllowed
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrOriginNotAllowed
	}
	host := strings.ToLower(u.Hostname())
	for _, src := range s.sources {
		if src.scheme != scheme || src.host != host {
			continue
		}
		if strings.HasSuffix(src.path, "/") {
			if strings.HasPrefix(u.Path, src.path) {
				return nil
			}
			continue
		}
		if u.Path == src.path {
			return nil
		}
	}
	return ErrOriginNotAllowed
}

// countingBody wraps a response body so every byte handed to the reader is
// charged to the shared budget, including bytes from failed attempts.
type countingBody struct {
	rc     io.ReadCloser
	budget *budget
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		if budgetErr := b.budget.consume(n); budgetErr != nil {
			// Stop delivering data immediately; the caller's deferred
			// Body.Close still releases the underlying connection.
			return n, budgetErr
		}
	}
	return n, err
}

func (b *countingBody) Close() error { return b.rc.Close() }

// budgetTransport wraps the real transport and attaches budget accounting to
// every response, including intermediate redirect responses.
type budgetTransport struct {
	base   http.RoundTripper
	budget *budget
}

func (t *budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &countingBody{rc: resp.Body, budget: t.budget}
	return resp, nil
}

// Option customizes a Capability at construction time.
type Option func(*Capability)

// WithMaxAttempts overrides the per-target attempt limit (initial try plus
// retries). Values below one are clamped to one.
func WithMaxAttempts(n int) Option {
	return func(c *Capability) { c.maxAttempts = n }
}

// WithTransport injects the underlying RoundTripper, the seam tests use to
// emulate failing transfers.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *Capability) { c.transport = rt }
}

// Capability is the sealed network grant handed to a build host. It is safe
// for concurrent use; the byte budget is shared across all concurrent fetches.
type Capability struct {
	sources     *SourceSet
	budget      *budget
	transport   http.RoundTripper
	client      *http.Client
	maxAttempts int
}

// New builds a Capability restricted to sources and a total receive budget of
// maxBytes. The budget covers every response byte of every attempt and
// redirect; retries never replenish it.
func New(sources *SourceSet, maxBytes int64, opts ...Option) (*Capability, error) {
	if sources == nil {
		return nil, errors.New("netsec: nil source set")
	}
	if maxBytes < 0 {
		return nil, errors.New("netsec: negative byte budget")
	}
	c := &Capability{
		sources:     sources,
		budget:      &budget{limit: maxBytes},
		maxAttempts: defaultMaxAttempts,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.maxAttempts < 1 {
		c.maxAttempts = 1
	}
	c.client = &http.Client{
		Transport: &budgetTransport{base: c.transport, budget: c.budget},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Every redirect hop must itself be inside the source set.
			if err := c.sources.allow(req.URL); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("netsec: stopped after 10 redirects")
			}
			return nil
		},
	}
	return c, nil
}

// BudgetLimit returns the configured maximum total received bytes.
func (c *Capability) BudgetLimit() int64 { return c.budget.limit }

// BudgetUsed returns bytes received so far, including bytes consumed by
// failed and retried attempts.
func (c *Capability) BudgetUsed() int64 { return c.budget.usedBytes() }

// BudgetRemaining returns the share of the budget still available.
func (c *Capability) BudgetRemaining() int64 { return c.budget.remaining() }

// Target is one authorized download: where to fetch it from, the verified
// output name and the mandatory SHA-256 digest (64 hex chars, optionally
// prefixed with "sha256:").
type Target struct {
	URL    string
	Name   string
	SHA256 string
}

// Fetch downloads every target into dir. No file is visible under dir until
// all targets have been fetched and verified: content is written to a private
// staging directory and only renamed into place after the whole batch passes
// digest verification. Any failure removes the staging area and leaves dir
// untouched.
func (c *Capability) Fetch(ctx context.Context, dir string, targets []Target) error {
	if len(targets) == 0 {
		return nil
	}

	type planned struct {
		target Target
		url    *url.URL
		digest []byte
	}
	plan := make([]planned, len(targets))
	for i, t := range targets {
		if err := validateName(t.Name); err != nil {
			return fmt.Errorf("target %d: %w", i, err)
		}
		digest, err := parseDigest(t.SHA256)
		if err != nil {
			return fmt.Errorf("target %q: %w", t.Name, err)
		}
		u, err := url.Parse(t.URL)
		if err != nil {
			return fmt.Errorf("target %q: %w: %v", t.Name, ErrOriginNotAllowed, err)
		}
		// Reject unauthorized URLs up front, before any socket is touched.
		if err := c.sources.allow(u); err != nil {
			return fmt.Errorf("target %q: %w", t.Name, err)
		}
		plan[i] = planned{target: t, url: u, digest: digest}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".netsec-stage-*")
	if err != nil {
		return err
	}
	// The staging directory is always cleaned up; successful files have been
	// renamed out of it by then.
	defer os.RemoveAll(stage)

	staged := make([]string, len(plan))
	for i, p := range plan {
		final := filepath.Join(stage, p.target.Name)
		if err := c.fetchOne(ctx, final, p.url, p.digest); err != nil {
			return fmt.Errorf("netsec: fetch %q: %w", p.target.Name, err)
		}
		staged[i] = final
	}

	// All content verified: publish the whole batch per file.
	for i, p := range plan {
		dst := filepath.Join(dir, p.target.Name)
		if err := os.Rename(staged[i], dst); err != nil {
			return fmt.Errorf("netsec: publish %q: %w", p.target.Name, err)
		}
	}
	return nil
}

// fetchOne runs up to c.maxAttempts attempts against one target. All attempts
// draw from the same budget.
func (c *Capability) fetchOne(ctx context.Context, stagedPath string, u *url.URL, want []byte) error {
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.attempt(ctx, stagedPath, u, want)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable(err) {
			return err
		}
	}
	return fmt.Errorf("exhausted after %d attempt(s): %w", c.maxAttempts, lastErr)
}

// retryable classifies errors. Security and integrity failures are terminal:
// retrying them can never help and must not spend more of the budget.
func retryable(err error) bool {
	switch {
	case errors.Is(err, ErrBudgetExceeded),
		errors.Is(err, ErrDigestMismatch),
		errors.Is(err, ErrOriginNotAllowed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return false
	}
	return true
}

// attempt performs one GET, streaming the body through the budget counter and
// the digest hash into a temporary file, which is promoted to stagedPath only
// on full verification.
func (c *Capability) attempt(ctx context.Context, stagedPath string, u *url.URL, want []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected http status %s", resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(stagedPath), ".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On any error path the partial file is removed; nothing is published.
	defer os.Remove(tmpName)

	hash := sha256.New()
	if _, err := copyCtx(ctx, tmp, io.TeeReader(resp.Body, hash)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	got := hash.Sum(nil)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return fmt.Errorf("%w: expected %s, got %s",
			ErrDigestMismatch, hex.EncodeToString(want), hex.EncodeToString(got))
	}
	// Verified content moves into its staging slot.
	return os.Rename(tmpName, stagedPath)
}

// copyCtx is io.Copy with context cancellation between reads, so cancelling a
// Fetch unblocks a stalled body instead of waiting for the next byte.
func copyCtx(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		nr, readErr := src.Read(buf)
		if nr > 0 {
			nw, writeErr := dst.Write(buf[:nr])
			if nw > 0 {
				total += int64(nw)
			}
			if writeErr != nil {
				return total, writeErr
			}
			if nw != nr {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return total, nil
			}
			return total, readErr
		}
	}
}

// parseDigest accepts "sha256:<64 hex>" or bare 64 hex and returns the raw
// digest bytes.
func parseDigest(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) != sha256.Size*2 {
		return nil, fmt.Errorf("%w: want %d hex chars", ErrBadDigest, sha256.Size*2)
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadDigest, err)
	}
	return raw, nil
}

// validateName only permits simple, non-path output names so a target cannot
// escape the output or staging directory.
func validateName(name string) error {
	if name == "" ||
		name == "." || name == ".." ||
		filepath.IsAbs(name) ||
		strings.ContainsAny(name, `/\`) ||
		name[0] == '.' {
		return fmt.Errorf("%w: %q", ErrBadTarget, name)
	}
	return nil
}
