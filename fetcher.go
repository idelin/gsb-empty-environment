package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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

// FetcherConfig configures a Fetcher. RepositoryClient may carry registry
// mTLS material. ExternalClient is cloned with cookie state removed, client
// certificates cleared, proxy credentials removed, redirect handling disabled,
// and automatic content decompression disabled.
type FetcherConfig struct {
	Policy            *ExternalURLPolicy
	TokenProvider     TokenProvider
	RepositoryClient  *http.Client
	ExternalClient    *http.Client
	PublicAccept      string
	PublicUserAgent   string
	RepositoryHeaders http.Header
	CacheDir          string
	TempDir           string
	MaxRedirects      int
}

// Fetcher obtains layers according to host-issued LayerPlan values.
type Fetcher struct {
	policy         *ExternalURLPolicy
	repoClient     *http.Client
	externalClient *http.Client
	tokens         TokenProvider
	accept         string
	userAgent      string
	repoHeaders    http.Header
	maxRedirects   int
	cacheDir       string
	ownsCacheDir   bool

	mu     sync.Mutex
	cache  map[Digest]*cachedBlob
	closed bool
}

type cachedBlob struct {
	digest    Digest
	path      string
	size      int64
	mediaType string
}

// NewFetcher validates host configuration and prepares an isolated cache.
func NewFetcher(cfg FetcherConfig) (*Fetcher, error) {
	if cfg.Policy == nil {
		return nil, fmt.Errorf("%w: URL policy is required", ErrURLPolicy)
	}
	if err := validateHeaderValue("Accept", cfg.PublicAccept); err != nil {
		return nil, err
	}
	if err := validateHeaderValue("User-Agent", cfg.PublicUserAgent); err != nil {
		return nil, err
	}
	repoHeaders, err := sanitizeRepositoryHeaders(cfg.RepositoryHeaders)
	if err != nil {
		return nil, err
	}
	if cfg.MaxRedirects < 0 {
		return nil, fmt.Errorf("max redirects must not be negative")
	}
	maxRedirects := cfg.MaxRedirects
	if maxRedirects == 0 {
		maxRedirects = DefaultMaxRedirects
	}

	externalClient, err := hardenedExternalClient(cfg.ExternalClient)
	if err != nil {
		return nil, err
	}
	repoClient, err := hardenedRepositoryClient(cfg.RepositoryClient)
	if err != nil {
		return nil, err
	}

	cacheDir := cfg.CacheDir
	ownsCacheDir := false
	if cacheDir == "" {
		cacheDir, err = os.MkdirTemp(cfg.TempDir, "netsec-layer-cache-")
		if err != nil {
			return nil, fmt.Errorf("create layer cache: %w", err)
		}
		ownsCacheDir = true
	} else {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("create layer cache: %w", err)
		}
	}

	return &Fetcher{
		policy:         cfg.Policy,
		repoClient:     repoClient,
		externalClient: externalClient,
		tokens:         cfg.TokenProvider,
		accept:         cfg.PublicAccept,
		userAgent:      cfg.PublicUserAgent,
		repoHeaders:    repoHeaders,
		maxRedirects:   maxRedirects,
		cacheDir:       cacheDir,
		ownsCacheDir:   ownsCacheDir,
		cache:          make(map[Digest]*cachedBlob),
	}, nil
}

// Plan builds a plan with the fetcher's host policy.
func (f *Fetcher) Plan(manifest ManifestRef, layer Layer) (*LayerPlan, error) {
	return BuildLayerPlan(f.policy, manifest, layer)
}

// FetchLayer builds and fetches a single layer.
func (f *Fetcher) FetchLayer(ctx context.Context, manifest ManifestRef, layer Layer) (io.ReadCloser, error) {
	plan, err := f.Plan(manifest, layer)
	if err != nil {
		return nil, err
	}
	return f.Fetch(ctx, plan)
}

// Fetch obtains a verified compressed layer. Bytes are streamed into a
// private partial file, checked for exact size and sha256, and only then
// published into the digest-keyed cache.
func (f *Fetcher) Fetch(ctx context.Context, plan *LayerPlan) (io.ReadCloser, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := f.checkOpen(); err != nil {
		return nil, err
	}
	repo, err := plan.Manifest.Repository.canonical()
	if err != nil {
		return nil, err
	}

	if rc, ok, err := f.openCached(plan.Layer.Digest, plan.Layer.Size); err != nil {
		return nil, err
	} else if ok {
		return rc, nil
	}

	var errs []error
	for i := range plan.Sources {
		src := plan.Sources[i].Clone()
		entry, fetchErr := f.fetchSource(ctx, repo, plan.Layer, src)
		if fetchErr != nil {
			errs = append(errs, fmt.Errorf("source %d (%s): %w", i, sourceForError(src), fetchErr))
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		rc, openErr := f.openEntry(entry, plan.Layer.Size)
		if openErr != nil {
			errs = append(errs, fmt.Errorf("source %d (%s): %w", i, sourceForError(src), openErr))
			continue
		}
		return rc, nil
	}
	return nil, fmt.Errorf("fetch layer %s: all %d sources failed: %w", plan.Layer.Digest, len(plan.Sources), errors.Join(errs...))
}

// Close releases the Fetcher's private cache directory. Verified blobs opened
// by callers remain readable until their readers are closed.
func (f *Fetcher) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.cache = make(map[Digest]*cachedBlob)
	dir := f.cacheDir
	remove := f.ownsCacheDir
	f.mu.Unlock()
	if remove {
		return os.RemoveAll(dir)
	}
	return nil
}

func (f *Fetcher) checkOpen() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	return nil
}

func (f *Fetcher) fetchSource(ctx context.Context, repo Repository, layer Layer, src Source) (*cachedBlob, error) {
	current, err := f.validateInitialSource(repo, layer.Digest, src)
	if err != nil {
		return nil, err
	}
	rootDomain := RegistrableDomain(current.Hostname())
	visited := map[string]struct{}{current.String(): {}}
	hop := 0

	var authorization string
	var tokenLoaded bool

	for {
		kind := SourceExternal
		if isRepositoryBlobURL(repo, current, layer.Digest) {
			kind = SourceRepository
		}

		if kind == SourceRepository && !tokenLoaded {
			tokenLoaded = true
			authorization, err = authorizeToken(ctx, f.tokens, repo, layer.Digest)
			if err != nil {
				return nil, err
			}
		}

		req, err := f.newRequest(ctx, current, kind, authorization)
		if err != nil {
			return nil, err
		}
		client := f.externalClient
		if kind == SourceRepository {
			client = f.repoClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request %s: %w", current.Redacted(), err)
		}

		if resp.StatusCode == http.StatusOK {
			return f.consumeVerified(resp, layer)
		}

		if !isRedirectStatus(resp.StatusCode) {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w: %s returned %s", ErrHTTPStatus, current.Redacted(), resp.Status)
		}

		location, err := singleRedirectLocation(resp)
		if err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		if hop >= f.maxRedirects {
			return nil, fmt.Errorf("%w: exceeded %d redirects", ErrHTTPStatus, f.maxRedirects)
		}
		next, err := f.resolveRedirect(current, rootDomain, repo, layer.Digest, location)
		if err != nil {
			return nil, err
		}
		if _, seen := visited[next.String()]; seen {
			return nil, fmt.Errorf("%w: redirect loop at %s", ErrURLPolicy, next.Redacted())
		}
		visited[next.String()] = struct{}{}
		current = next
		hop++
	}
}

func (f *Fetcher) validateInitialSource(repo Repository, digest Digest, src Source) (*url.URL, error) {
	if src.URL == nil {
		return nil, fmt.Errorf("%w: source has no URL", ErrInvalidPlan)
	}
	raw := src.URL.String()
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return nil, fmt.Errorf("%w: source URL contains whitespace or control characters", ErrURLPolicy)
	}
	if strings.Contains(raw, "#") {
		return nil, fmt.Errorf("%w: source URL contains a fragment", ErrURLPolicy)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: source URL: %v", ErrURLPolicy, err)
	}
	if err := validateCommonHTTPSURL(u); err != nil {
		return nil, err
	}
	if src.Kind == SourceRepository {
		if !isRepositoryBlobURL(repo, u, digest) {
			return nil, fmt.Errorf("%w: repository source is not the exact blob URL", ErrInvalidPlan)
		}
		return cloneURL(u), nil
	}
	if isRepositoryBlobURL(repo, u, digest) {
		return cloneURL(u), nil
	}
	if isRepositoryOriginURL(repo, u) {
		return cloneURL(u), nil
	}
	return f.policy.ValidateURL(raw)
}

func (f *Fetcher) resolveRedirect(from *url.URL, rootDomain string, repo Repository, digest Digest, location string) (*url.URL, error) {
	if location == "" {
		return nil, fmt.Errorf("%w: empty redirect location", ErrHTTPStatus)
	}
	if strings.ContainsAny(location, "\x00\r\n\t ") {
		return nil, fmt.Errorf("%w: redirect location contains whitespace or control characters", ErrURLPolicy)
	}
	if strings.Contains(location, "#") {
		return nil, fmt.Errorf("%w: redirect location contains a fragment", ErrURLPolicy)
	}
	ref, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("%w: redirect location: %v", ErrURLPolicy, err)
	}
	next := from.ResolveReference(ref)
	if err := validateCommonHTTPSURL(next); err != nil {
		return nil, err
	}
	if isRepositoryBlobURL(repo, next, digest) {
		if RegistrableDomain(next.Hostname()) != rootDomain {
			return nil, fmt.Errorf("%w: redirect crosses registrable domains", ErrURLPolicy)
		}
		return cloneURL(next), nil
	}
	if isRepositoryOriginURL(repo, next) {
		// The host-fixed repository origin is trusted even when the redirect
		// leaves the canonical blob path. It remains tokenless below.
		if RegistrableDomain(next.Hostname()) != rootDomain {
			return nil, fmt.Errorf("%w: redirect crosses registrable domains", ErrURLPolicy)
		}
		return cloneURL(next), nil
	}
	canonical, err := f.policy.ValidateURL(next.String())
	if err != nil {
		return nil, err
	}
	if RegistrableDomain(canonical.Hostname()) != rootDomain {
		return nil, fmt.Errorf("%w: redirect crosses registrable domains (%s -> %s)", ErrURLPolicy, rootDomain, RegistrableDomain(canonical.Hostname()))
	}
	return canonical, nil
}

func (f *Fetcher) newRequest(ctx context.Context, u *url.URL, kind SourceKind, authorization string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header = make(http.Header)
	if f.accept != "" {
		req.Header.Set("Accept", f.accept)
	}
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	} else {
		// An explicitly present empty value suppresses net/http's default UA.
		req.Header["User-Agent"] = []string{""}
	}
	if kind == SourceRepository {
		for name, values := range f.repoHeaders {
			req.Header[name] = append([]string(nil), values...)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
	}
	return req, nil
}

func (f *Fetcher) consumeVerified(resp *http.Response, layer Layer) (*cachedBlob, error) {
	defer func() { _ = resp.Body.Close() }()
	for _, value := range resp.Header.Values("Content-Encoding") {
		for _, encoding := range strings.Split(value, ",") {
			encoding = strings.TrimSpace(encoding)
			if encoding != "" && !strings.EqualFold(encoding, "identity") {
				return nil, fmt.Errorf("%w: %s", ErrContentEncoding, encoding)
			}
		}
	}

	tmp, err := os.CreateTemp(f.cacheDir, ".partial-*")
	if err != nil {
		return nil, fmt.Errorf("create isolated layer file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	var count int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if count+int64(n) > layer.Size {
				return nil, fmt.Errorf("%w: body exceeds declared size %d", ErrSizeMismatch, layer.Size)
			}
			if err := writeFull(tmp, buf[:n]); err != nil {
				return nil, fmt.Errorf("write isolated layer: %w", err)
			}
			if _, err := hasher.Write(buf[:n]); err != nil {
				return nil, fmt.Errorf("hash isolated layer: %w", err)
			}
			count += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read layer response: %w", readErr)
		}
	}
	if count != layer.Size {
		return nil, fmt.Errorf("%w: got %d compressed bytes, want %d", ErrSizeMismatch, count, layer.Size)
	}
	sum := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if sum != string(layer.Digest) {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, sum, layer.Digest)
	}
	if err := tmp.Sync(); err != nil {
		return nil, fmt.Errorf("sync isolated layer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close isolated layer: %w", err)
	}
	closed = true

	entry, err := f.publish(tmpName, layer)
	if err != nil {
		return nil, err
	}
	committed = true
	return entry, nil
}

func (f *Fetcher) publish(tmpName string, layer Layer) (*cachedBlob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, ErrClosed
	}
	if entry := f.cache[layer.Digest]; entry != nil {
		_ = os.Remove(tmpName)
		if entry.size != layer.Size {
			return nil, fmt.Errorf("%w: verified blob size %d, plan declares %d", ErrSizeMismatch, entry.size, layer.Size)
		}
		return entry, nil
	}
	finalPath := filepath.Join(f.cacheDir, "sha256-"+layer.Digest.Hex()+".blob")
	if err := os.Rename(tmpName, finalPath); err != nil {
		return nil, fmt.Errorf("publish verified layer: %w", err)
	}
	entry := &cachedBlob{
		digest:    layer.Digest,
		path:      finalPath,
		size:      layer.Size,
		mediaType: layer.MediaType,
	}
	f.cache[layer.Digest] = entry
	return entry, nil
}

func (f *Fetcher) openCached(digest Digest, size int64) (io.ReadCloser, bool, error) {
	f.mu.Lock()
	entry := f.cache[digest]
	if entry == nil {
		f.mu.Unlock()
		return nil, false, nil
	}
	if entry.size != size {
		f.mu.Unlock()
		return nil, false, fmt.Errorf("%w: cached blob size %d, plan declares %d", ErrSizeMismatch, entry.size, size)
	}
	path := entry.path
	f.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		f.mu.Lock()
		if current := f.cache[digest]; current != nil && current.path == path {
			delete(f.cache, digest)
		}
		f.mu.Unlock()
		return nil, false, nil
	}
	if info, err := file.Stat(); err != nil || info.Size() != entry.size {
		_ = file.Close()
		f.mu.Lock()
		if current := f.cache[digest]; current != nil && current.path == path {
			delete(f.cache, digest)
		}
		f.mu.Unlock()
		return nil, false, nil
	}
	return file, true, nil
}

func (f *Fetcher) openEntry(entry *cachedBlob, size int64) (io.ReadCloser, error) {
	if entry == nil {
		return nil, fmt.Errorf("%w: missing verified blob entry", ErrInvalidPlan)
	}
	if entry.size != size {
		return nil, fmt.Errorf("%w: verified blob size %d, plan declares %d", ErrSizeMismatch, entry.size, size)
	}
	file, err := os.Open(entry.path)
	if err != nil {
		f.mu.Lock()
		if current := f.cache[entry.digest]; current == entry {
			delete(f.cache, entry.digest)
		}
		f.mu.Unlock()
		return nil, fmt.Errorf("open verified layer: %w", err)
	}
	if info, err := file.Stat(); err != nil || info.Size() != entry.size {
		_ = file.Close()
		f.mu.Lock()
		if current := f.cache[entry.digest]; current == entry {
			delete(f.cache, entry.digest)
		}
		f.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("stat verified layer: %w", err)
		}
		return nil, fmt.Errorf("%w: verified file is truncated", ErrSizeMismatch)
	}
	return file, nil
}

func validateHeaderValue(name, value string) error {
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("invalid %s header value", name)
		}
	}
	return nil
}

func sanitizeRepositoryHeaders(in http.Header) (http.Header, error) {
	out := make(http.Header)
	for name, values := range in {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" {
			return nil, fmt.Errorf("invalid repository header name %q", name)
		}
		switch canonical {
		case "Authorization", "Cookie", "Cookie2", "Proxy-Authorization", "Host",
			"Content-Length", "Transfer-Encoding", "Connection", "Trailer", "Upgrade",
			"Accept", "User-Agent":
			return nil, fmt.Errorf("repository header %s is controlled by the fetcher", canonical)
		}
		copyValues := make([]string, 0, len(values))
		for _, value := range values {
			if err := validateHeaderValue(canonical, value); err != nil {
				return nil, err
			}
			copyValues = append(copyValues, value)
		}
		if len(copyValues) > 0 {
			out[canonical] = copyValues
		}
	}
	return out, nil
}

func clientWithoutRedirects(in *http.Client) *http.Client {
	client := &http.Client{}
	if in != nil {
		*client = *in
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func hardenedRepositoryClient(in *http.Client) (*http.Client, error) {
	client := clientWithoutRedirects(in)
	var transport *http.Transport
	switch current := client.Transport.(type) {
	case nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("%w: default transport is not *http.Transport", ErrUnsafeExternalTransport)
		}
		transport = defaultTransport.Clone()
	case *http.Transport:
		transport = current.Clone()
	}
	if transport != nil {
		transport.DisableCompression = true
		client.Transport = transport
	}
	return client, nil
}

func hardenedExternalClient(in *http.Client) (*http.Client, error) {
	client := clientWithoutRedirects(in)
	var transport *http.Transport
	switch current := client.Transport.(type) {
	case nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("%w: default transport is not *http.Transport", ErrUnsafeExternalTransport)
		}
		transport = defaultTransport.Clone()
	case *http.Transport:
		transport = current.Clone()
	default:
		return nil, fmt.Errorf("%w: transport must be *http.Transport", ErrUnsafeExternalTransport)
	}
	if transport.DialTLSContext != nil || transport.DialTLS != nil {
		return nil, fmt.Errorf("%w: custom TLS dialers can attach client certificates", ErrUnsafeExternalTransport)
	}
	transport.DisableCompression = true
	transport.ForceAttemptHTTP2 = false
	transport.Proxy = nil
	transport.ProxyConnectHeader = nil
	transport.TLSClientConfig = clearClientCertificates(transport.TLSClientConfig)
	client.Transport = transport
	return client, nil
}

func clearClientCertificates(in *tls.Config) *tls.Config {
	if in == nil {
		return nil
	}
	out := in.Clone()
	out.Certificates = nil
	out.NameToCertificate = nil
	out.GetClientCertificate = nil
	out.GetConfigForClient = nil
	return out
}

func writeFull(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func singleRedirectLocation(resp *http.Response) (string, error) {
	locations := resp.Header.Values("Location")
	if len(locations) != 1 || strings.TrimSpace(locations[0]) == "" {
		return "", fmt.Errorf("%w: redirect response requires exactly one Location", ErrHTTPStatus)
	}
	return strings.TrimSpace(locations[0]), nil
}

func sourceForError(src Source) string {
	if src.URL == nil {
		return "<nil>"
	}
	return src.URL.Redacted()
}
