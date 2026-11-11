package depseal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// DefaultMaxBlobSize caps a single package blob at 256 MiB.
const DefaultMaxBlobSize int64 = 256 << 20

// maxHTTPRedirects mirrors the standard client limit, but every hop is
// additionally checked against the source host allowlist.
const maxHTTPRedirects = 10

// Result is the outcome of a successful fetch.
type Result struct {
	Package Package
	Source  Source
	Content []byte
	// Cached is true when the content was served from the tenant-scoped
	// cache rather than downloaded.
	Cached bool
	// Attempts counts download attempts (1 on the first try).
	Attempts int
}

// Fetcher resolves locked packages while enforcing source identity, digest
// verification and tenant-scoped caching. A Fetcher is safe for concurrent
// use; parallel calls for the same blob share a single in-flight download and
// reuse the same verified cache entry.
type Fetcher struct {
	lock       *Lockfile
	authz      Authorizer
	httpClient *http.Client
	cache      *contentCache

	// retries is the number of additional attempts after a failed one.
	retries int
	// backoff is the base delay between attempts; attempt n waits
	// backoff * 2^(n-1).
	backoff time.Duration
	maxSize int64

	inflightMu sync.Mutex
	inflight   map[cacheKey]*inflightCall
}

type inflightCall struct {
	done  chan struct{}
	entry cacheEntry
	err   error
}

// Option customizes a Fetcher.
type Option func(*Fetcher)

// WithRetries configures how many times a failed attempt is retried.
func WithRetries(retries int) Option {
	return func(f *Fetcher) {
		if retries >= 0 {
			f.retries = retries
		}
	}
}

// WithBackoff configures the base retry delay.
func WithBackoff(backoff time.Duration) Option {
	return func(f *Fetcher) {
		if backoff >= 0 {
			f.backoff = backoff
		}
	}
}

// WithMaxBlobSize overrides the maximum accepted blob size.
func WithMaxBlobSize(size int64) Option {
	return func(f *Fetcher) {
		if size > 0 {
			f.maxSize = size
		}
	}
}

// WithHTTPClient injects a custom HTTP client. Its CheckRedirect is replaced
// so cross-source redirects can be enforced.
func WithHTTPClient(client *http.Client) Option {
	return func(f *Fetcher) {
		f.httpClient = client
	}
}

// NewFetcher builds a fetcher for a validated lock file.
func NewFetcher(lock *Lockfile, authz Authorizer, opts ...Option) *Fetcher {
	f := &Fetcher{
		lock:     lock,
		authz:    authz,
		cache:    newContentCache(),
		retries:  2,
		backoff:  100 * time.Millisecond,
		maxSize:  DefaultMaxBlobSize,
		inflight: make(map[cacheKey]*inflightCall),
	}
	for _, opt := range opts {
		opt(f)
	}
	if f.httpClient == nil {
		f.httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	f.httpClient.CheckRedirect = f.checkRedirect
	return f
}

// checkRedirect rejects any hop whose target host is outside the source
// stored on the request context. This is the core defense against a private
// source redirecting to a same-named public package.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHTTPRedirects {
		return fmt.Errorf("depseal: stopped after %d redirects", maxHTTPRedirects)
	}
	source, ok := sourceFromContext(req.Context())
	if !ok {
		return errors.New("depseal: redirect without locked source context")
	}
	from := req.Response.Request.URL.Host
	if !source.allowsHost(req.URL.Host) {
		return &CrossSourceError{
			SourceID: source.ID,
			FromHost: from,
			ToHost:   req.URL.Host,
		}
	}
	return nil
}

// Fetch resolves a name@version coordinate from the lock file.
func (f *Fetcher) Fetch(ctx context.Context, tenant, name, version string) (*Result, error) {
	pkg, ok := f.lock.Lookup(name, version)
	if !ok {
		return nil, fmt.Errorf("depseal: package %s@%s is not in the lock file", name, version)
	}
	return f.FetchPackage(ctx, tenant, pkg)
}

// FetchPackage resolves a package entry from the lock file.
func (f *Fetcher) FetchPackage(ctx context.Context, tenant string, pkg Package) (result *Result, err error) {
	source, ok := f.lock.Sources[pkg.Source]
	if !ok {
		return nil, &FetchError{Package: pkg, Parent: pkg.Parent,
			Err: fmt.Errorf("unknown locked source %q", pkg.Source)}
	}

	if !f.authz.Allowed(tenant, source.ID) {
		return nil, &FetchError{Package: pkg, Parent: pkg.Parent,
			Err: &TenantDeniedError{Tenant: tenant, SourceID: source.ID}}
	}

	key := cacheKey{tenant: tenant, sourceID: source.ID, digest: pkg.Digest}

	if content, cached := f.cache.get(key); cached {
		return &Result{Package: pkg, Source: source, Content: content, Cached: true, Attempts: 0}, nil
	}

	call := f.joinOrStart(ctx, key, func(callCtx context.Context) (cacheEntry, error) {
		return f.fetchVerified(callCtx, source, pkg)
	})
	if call.err != nil {
		return nil, &FetchError{Package: pkg, Parent: pkg.Parent, Err: call.err}
	}

	content := make([]byte, len(call.entry.content))
	copy(content, call.entry.content)
	return &Result{Package: pkg, Source: source, Content: content, Attempts: call.attempts()}, nil
}

// joinOrStart deduplicates concurrent fetches of the same tenant/source/
// digest blob: the first caller performs the download while the others wait
// and share its verified result.
func (f *Fetcher) joinOrStart(ctx context.Context, key cacheKey, download func(context.Context) (cacheEntry, error)) *inflightCall {
	f.inflightMu.Lock()
	if call, ok := f.inflight[key]; ok {
		f.inflightMu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return &inflightCall{err: ctx.Err()}
		}
		return call
	}
	call := &inflightCall{done: make(chan struct{})}
	f.inflight[key] = call
	f.inflightMu.Unlock()

	go func() {
		defer close(call.done)
		entry, err := download(ctx)
		if err == nil {
			// Only verified content is ever published, so waiters cannot
			// observe an unchecked blob.
			f.cache.put(key, entry)
		}
		call.entry, call.err = entry, err

		f.inflightMu.Lock()
		delete(f.inflight, key)
		f.inflightMu.Unlock()
	}()

	select {
	case <-call.done:
		return call
	case <-ctx.Done():
		return &inflightCall{err: ctx.Err()}
	}
}

func (c *inflightCall) attempts() int { return c.entry.attempts }

// fetchVerified downloads a blob with retries, verifying the digest on every
// attempt. Cross-source and authorization failures are terminal; transport
// failures, bad statuses and digest mismatches are retried.
func (f *Fetcher) fetchVerified(ctx context.Context, source Source, pkg Package) (cacheEntry, error) {
	var lastErr error
	for attempt := 1; attempt <= f.retries+1; attempt++ {
		if attempt > 1 {
			delay := f.backoff << (attempt - 2)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return cacheEntry{}, ctx.Err()
			}
		}

		content, err := f.download(ctx, source, pkg)
		if err == nil {
			if err = pkg.Digest.verify(content); err == nil {
				return cacheEntry{sourceID: source.ID, digest: pkg.Digest, content: content, attempts: attempt}, nil
			}
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return cacheEntry{}, fmt.Errorf("package %s: %w", pkg.Identity(), lastErr)
}

func (f *Fetcher) download(ctx context.Context, source Source, pkg Package) ([]byte, error) {
	target, err := source.packageURL(pkg)
	if err != nil {
		return nil, err
	}
	reqCtx := contextWithSource(ctx, source)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{SourceID: source.ID, Status: resp.StatusCode}
	}

	body := io.LimitReader(resp.Body, f.maxSize+1)
	content, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > f.maxSize {
		return nil, fmt.Errorf("package %s exceeds max blob size %d", pkg.Identity(), f.maxSize)
	}
	return content, nil
}

func retryable(err error) bool {
	var cross *CrossSourceError
	if errors.As(err, &cross) {
		return false
	}
	var denied *TenantDeniedError
	if errors.As(err, &denied) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var mismatch *DigestMismatchError
	if errors.As(err, &mismatch) {
		return true
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		// Retry transient client statuses and every 5xx; other 4xx errors
		// (auth problems, missing blobs) will not change on retry.
		if statusErr.Status == http.StatusRequestTimeout ||
			statusErr.Status == http.StatusTooManyRequests ||
			statusErr.Status >= 500 {
			return true
		}
		return false
	}
	return err != nil
}

// FetchAll fetches every package in the lock file in parallel, bounded by
// workers. Verified blobs fetched for one package are shared through the
// cache with concurrent requests for the same content.
func (f *Fetcher) FetchAll(ctx context.Context, tenant string, workers int) (map[string]*Result, error) {
	if workers <= 0 {
		workers = 4
	}
	packages := f.lock.Packages
	results := make(map[string]*Result, len(packages))
	type indexed struct {
		idx int
		pkg Package
	}
	jobs := make(chan indexed)

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)

	for i := 0; i < workers && i < len(packages); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				res, err := f.FetchPackage(ctx, tenant, job.pkg)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else {
					results[job.pkg.Identity()] = res
				}
				mu.Unlock()
			}
		}()
	}

	for i, pkg := range packages {
		select {
		case jobs <- indexed{idx: i, pkg: pkg}:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return results, firstErr
}
