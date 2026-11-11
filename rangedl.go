package rangedl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrVersionChanged means the server file no longer matches the ETag the
	// in-progress download was started against.
	ErrVersionChanged = errors.New("rangedl: server file version changed (ETag mismatch)")
	// ErrContentRange means the server returned a Content-Range that does not
	// match the requested byte interval.
	ErrContentRange = errors.New("rangedl: invalid Content-Range in response")
	// ErrChecksum means the fully downloaded file does not match the expected
	// digest.
	ErrChecksum = errors.New("rangedl: checksum mismatch")
	// ErrSize means the fully downloaded file does not match the expected size.
	ErrSize = errors.New("rangedl: final size mismatch")
	// ErrUnsupported means the server neither honors Range nor returns the
	// whole entity with a usable length.
	ErrUnsupported = errors.New("rangedl: server does not support resumable downloads")
)

// Config configures a Downloader.
type Config struct {
	// Concurrency is the number of parallel chunk requests. Default 4.
	Concurrency int
	// ChunkSize is the target size of a single byte-range request. Default 8MiB.
	ChunkSize int64
	// MaxChunkAttempts bounds retries of a single chunk request. Default 8.
	MaxChunkAttempts int
	// MaxRestarts bounds full restarts caused by server-side version changes.
	// Default 3.
	MaxRestarts int
	// RetryDelay is the base exponential backoff delay. Default 200ms.
	RetryDelay time.Duration
	// ExpectedSize, when > 0, is additionally checked against the final file.
	ExpectedSize int64
	// ExpectedSHA256 is an optional lowercase hex digest checked at the end.
	ExpectedSHA256 string
	// HTTPClient is used for all requests. A default client is created when nil.
	HTTPClient *http.Client
}

// Result describes a completed download.
type Result struct {
	Path         string
	Size         int64
	ETag         string
	LastModified string
	SHA256       string
	Restarted    bool
}

// Downloader performs resumable, concurrent range downloads.
type Downloader struct {
	cfg  Config
	doer *http.Client
}

// NewDownloader builds a Downloader with defaults applied.
func NewDownloader(cfg Config) *Downloader {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 8 << 20
	}
	if cfg.MaxChunkAttempts <= 0 {
		cfg.MaxChunkAttempts = 8
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 200 * time.Millisecond
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Downloader{cfg: cfg, doer: client}
}

// chunkState is the on-disk record of one byte interval.
type chunkState struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Done  bool  `json:"done"`
}

// downloadState is atomically persisted alongside the partial file.
type downloadState struct {
	URL          string       `json:"url"`
	ETag         string       `json:"etag"`
	LastModified string       `json:"last_modified"`
	TotalSize    int64        `json:"total_size"`
	AcceptRanges bool         `json:"accept_ranges"`
	Chunks       []chunkState `json:"chunks"`
}

// saveState writes the state file atomically: temp file + fsync + rename + fsync.
func (d *Downloader) saveState(path string, st *downloadState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("rangedl: marshal state: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("rangedl: create state temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
	return nil
}

func loadState(path string) (*downloadState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st downloadState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("rangedl: corrupt state file %s: %w", path, err)
	}
	return &st, nil
}

// planChunks splits [0, size) into fixed-size intervals.
func planChunks(size, chunkSize int64) []chunkState {
	if size <= 0 {
		return nil
	}
	n := (size + chunkSize - 1) / chunkSize
	chunks := make([]chunkState, 0, n)
	for start := int64(0); start < size; start += chunkSize {
		end := start + chunkSize - 1
		if end >= size {
			end = size - 1
		}
		chunks = append(chunks, chunkState{Start: start, End: end})
	}
	return chunks
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// backoff returns the delay before retrying the (zero-based) failed attempt.
func (d *Downloader) backoff(attempt int) time.Duration {
	base := d.cfg.RetryDelay << attempt
	if base <= 0 || base > 10*time.Second {
		base = 10 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(d.cfg.RetryDelay)/2 + 1))
	return base + jitter
}

// statusError carries an HTTP status so callers can decide retryability.
type statusError struct {
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("rangedl: unexpected HTTP status %d", e.status)
}

// isRetryable reports whether a request error should be retried. Version
// changes, Content-Range violations and permanent 4xx errors are fatal.
func isRetryable(err error) bool {
	if errors.Is(err, ErrVersionChanged) ||
		errors.Is(err, ErrContentRange) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		switch se.status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusGone:
			return false
		}
	}
	return true
}

// probe performs a one-byte ranged GET to learn ETag, Last-Modified, total
// size and whether the server honors Range. Transient failures are retried.
func (d *Downloader) probe(ctx context.Context, url string) (etag, lastMod string, size int64, rangesOK bool, err error) {
	for attempt := 0; attempt < d.cfg.MaxChunkAttempts; attempt++ {
		if attempt > 0 {
			if err = sleepCtx(ctx, d.backoff(attempt-1)); err != nil {
				return "", "", 0, false, err
			}
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			return "", "", 0, false, reqErr
		}
		req.Header.Set("Range", "bytes=0-0")
		resp, doErr := d.doer.Do(req)
		if doErr != nil {
			if !isRetryable(doErr) || attempt == d.cfg.MaxChunkAttempts-1 {
				return "", "", 0, false, doErr
			}
			continue
		}
		etag = resp.Header.Get("ETag")
		lastMod = resp.Header.Get("Last-Modified")
		switch resp.StatusCode {
		case http.StatusPartialContent:
			_, total, perr := parseContentRange(resp.Header.Get("Content-Range"))
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
			resp.Body.Close()
			if perr != nil {
				if attempt == d.cfg.MaxChunkAttempts-1 {
					return "", "", 0, false, perr
				}
				continue
			}
			return etag, lastMod, total, true, nil
		case http.StatusOK:
			size = resp.ContentLength
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return etag, lastMod, size, false, nil
		default:
			resp.Body.Close()
			se := &statusError{status: resp.StatusCode}
			if !isRetryable(se) || attempt == d.cfg.MaxChunkAttempts-1 {
				return "", "", 0, false, se
			}
		}
	}
	return "", "", 0, false, fmt.Errorf("rangedl: probe exhausted retries for %s", url)
}

// parseContentRange parses "bytes start-end/total" and returns (start,total).
func parseContentRange(cr string) (start, total int64, err error) {
	const prefix = "bytes "
	if !strings.HasPrefix(cr, prefix) {
		return 0, 0, fmt.Errorf("%w: %q", ErrContentRange, cr)
	}
	body := strings.TrimPrefix(cr, prefix)
	slash := strings.LastIndexByte(body, '/')
	if slash < 0 {
		return 0, 0, fmt.Errorf("%w: %q", ErrContentRange, cr)
	}
	interval := body[:slash]
	totalStr := body[slash+1:]
	dash := strings.IndexByte(interval, '-')
	if dash < 0 {
		return 0, 0, fmt.Errorf("%w: %q", ErrContentRange, cr)
	}
	start, err = strconv.ParseInt(interval[:dash], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %q: %v", ErrContentRange, cr, err)
	}
	if totalStr != "*" {
		total, err = strconv.ParseInt(totalStr, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("%w: %q: %v", ErrContentRange, cr, err)
		}
	}
	return start, total, nil
}

// sectionWriter writes at absolute file offsets via WriteAt.
type sectionWriter struct {
	f      *os.File
	offset int64
}

func (w *sectionWriter) Write(p []byte) (int, error) {
	n, err := w.f.WriteAt(p, w.offset)
	w.offset += int64(n)
	return n, err
}

// errDegraded is internal: a chunk request got a 200, switch to full download.
var errDegraded = errors.New("rangedl: server ignored Range, degrading to full download")

// fetchChunk downloads one interval with If-Range pinned to the version.
func (d *Downloader) fetchChunk(ctx context.Context, url string, st *downloadState, idx int, part *os.File) error {
	c := st.Chunks[idx]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", c.Start, c.End))
	if st.ETag != "" {
		req.Header.Set("If-Range", st.ETag)
	} else if st.LastModified != "" {
		req.Header.Set("If-Range", st.LastModified)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent:
		gotStart, gotTotal, perr := parseContentRange(resp.Header.Get("Content-Range"))
		if perr != nil {
			return perr
		}
		if gotStart != c.Start || (gotTotal > 0 && gotTotal != st.TotalSize) {
			return fmt.Errorf("%w: got start=%d total=%d, want start=%d total=%d",
				ErrContentRange, gotStart, gotTotal, c.Start, st.TotalSize)
		}
		if et := resp.Header.Get("ETag"); et != "" && st.ETag != "" && et != st.ETag {
			return ErrVersionChanged
		}
		sw := &sectionWriter{f: part, offset: c.Start}
		n, copyErr := io.Copy(sw, resp.Body)
		if copyErr != nil {
			return copyErr
		}
		if want := c.End - c.Start + 1; n != want {
			return fmt.Errorf("%w: delivered %d bytes, want %d", ErrContentRange, n, want)
		}
		return nil

	case resp.StatusCode == http.StatusOK:
		// Server ignored Range (or ignored If-Range): the whole entity is
		// coming back. Signal the degraded full-download path.
		if et := resp.Header.Get("ETag"); et != "" && st.ETag != "" && et != st.ETag {
			return ErrVersionChanged
		}
		return errDegraded

	case resp.StatusCode == http.StatusPreconditionFailed:
		return ErrVersionChanged

	default:
		return &statusError{status: resp.StatusCode}
	}
}

// fetchFull streams the whole entity into the partial file from offset zero
// (degraded path when the server does not honor Range).
func (d *Downloader) fetchFull(ctx context.Context, url, etag, lastMod string, part *os.File) (int64, error) {
	if err := part.Truncate(0); err != nil {
		return 0, err
	}
	var written int64
	for attempt := 0; attempt < d.cfg.MaxChunkAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, d.backoff(attempt-1)); err != nil {
				return written, err
			}
			if err := part.Truncate(0); err != nil {
				return written, err
			}
			written = 0
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return written, err
		}
		resp, err := d.doer.Do(req)
		if err != nil {
			if !isRetryable(err) || attempt == d.cfg.MaxChunkAttempts-1 {
				return written, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			se := &statusError{status: resp.StatusCode}
			if !isRetryable(se) || attempt == d.cfg.MaxChunkAttempts-1 {
				return written, se
			}
			continue
		}
		if et := resp.Header.Get("ETag"); et != "" && etag != "" && et != etag {
			resp.Body.Close()
			return written, ErrVersionChanged
		}
		if lastMod == "" {
			lastMod = resp.Header.Get("Last-Modified")
		}
		n, copyErr := io.Copy(part, resp.Body)
		resp.Body.Close()
		written = n
		if copyErr != nil {
			if !isRetryable(copyErr) || attempt == d.cfg.MaxChunkAttempts-1 {
				return written, copyErr
			}
			continue
		}
		if resp.ContentLength >= 0 && n != resp.ContentLength {
			return written, fmt.Errorf("rangedl: truncated full download: got %d, want %d", n, resp.ContentLength)
		}
		if err := part.Sync(); err != nil {
			return written, err
		}
		return written, nil
	}
	return written, fmt.Errorf("rangedl: full download exhausted retries")
}

func partPaths(dest string) (part, state string) {
	return dest + ".part", dest + ".part.json"
}

func removeIfExists(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Best effort; rename/verify below will surface real problems.
	}
}

// Download (re)starts a download of url into dest. Existing partial state is
// resumed when it still matches the current server version.
func (d *Downloader) Download(ctx context.Context, url, dest string) (*Result, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("rangedl: create destination dir: %w", err)
	}
	partPath, statePath := partPaths(dest)

	var restarted bool
	for attempt := 0; ; attempt++ {
		st, err := d.runOnce(ctx, url, dest, partPath, statePath, attempt > 0)
		if err == nil {
			st.Restarted = restarted
			return st, nil
		}
		if errors.Is(err, ErrVersionChanged) && attempt < d.cfg.MaxRestarts {
			restarted = true
			removeIfExists(partPath)
			removeIfExists(statePath)
			if sleepErr := sleepCtx(ctx, d.backoff(0)); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		return nil, err
	}
}

func (d *Downloader) runOnce(ctx context.Context, url, dest, partPath, statePath string, isRestart bool) (*Result, error) {
	etag, lastMod, totalSize, rangesOK, err := d.probe(ctx, url)
	if err != nil {
		return nil, err
	}

	var st *downloadState
	if existing, loadErr := loadState(statePath); loadErr == nil {
		switch {
		case existing.URL != url:
			st = nil // different target: start over
		case existing.ETag != "" && etag != "" && existing.ETag != etag:
			return nil, ErrVersionChanged
		case existing.TotalSize != totalSize:
			return nil, ErrVersionChanged
		default:
			st = existing
		}
	} else if loadErr != nil && !os.IsNotExist(loadErr) {
		return nil, loadErr
	}

	if st == nil {
		st = &downloadState{
			URL:          url,
			ETag:         etag,
			LastModified: lastMod,
			TotalSize:    totalSize,
			AcceptRanges: rangesOK,
			Chunks:       planChunks(totalSize, d.cfg.ChunkSize),
		}
		if err := d.saveState(statePath, st); err != nil {
			return nil, err
		}
	} else if st.ETag == "" && etag != "" {
		st.ETag = etag
		st.LastModified = lastMod
	}

	part, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	defer part.Close()
	if totalSize > 0 {
		if err := preallocate(part, totalSize); err != nil {
			return nil, err
		}
	}

	if !rangesOK || !st.AcceptRanges {
		if totalSize < 0 {
			return nil, fmt.Errorf("%w: chunked 200 response without Content-Length", ErrUnsupported)
		}
		n, ferr := d.fetchFull(ctx, url, st.ETag, st.LastModified, part)
		if ferr != nil {
			return nil, ferr
		}
		st.TotalSize = n
		for i := range st.Chunks {
			st.Chunks[i].Done = true
		}
		st.AcceptRanges = false
		if err := d.saveState(statePath, st); err != nil {
			return nil, err
		}
	} else {
		if err := d.runChunks(ctx, url, st, part, statePath); err != nil {
			if !errors.Is(err, errDegraded) {
				return nil, err
			}
			// A worker discovered the server ignores Range mid-flight.
			// Restart the whole entity via plain GET.
			st.AcceptRanges = false
			n, ferr := d.fetchFull(ctx, url, st.ETag, st.LastModified, part)
			if ferr != nil {
				return nil, ferr
			}
			st.TotalSize = n
			for i := range st.Chunks {
				st.Chunks[i].Done = true
			}
			if err := d.saveState(statePath, st); err != nil {
				return nil, err
			}
		}
	}

	return d.finalize(ctx, dest, partPath, statePath, st)
}

// runChunks fetches all unfinished chunks with a bounded worker pool. Each
// completed chunk is fsynced and recorded atomically before the next one is
// claimed, so a crash never loses committed progress.
func (d *Downloader) runChunks(ctx context.Context, url string, st *downloadState, part *os.File, statePath string) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	setErr := func(e error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		mu.Unlock()
		cancel()
	}

	for w := 0; w < d.cfg.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if workCtx.Err() != nil {
					return
				}
				var attempt int
				for {
					err := d.fetchChunk(workCtx, url, st, idx, part)
					if err == nil {
						break
					}
					if errors.Is(err, errDegraded) {
						setErr(errDegraded)
						return
					}
					if !isRetryable(err) || attempt >= d.cfg.MaxChunkAttempts-1 || workCtx.Err() != nil {
						setErr(err)
						return
					}
					attempt++
					if sleepErr := sleepCtx(workCtx, d.backoff(attempt-1)); sleepErr != nil {
						setErr(sleepErr)
						return
					}
				}
				// Persist data first, then the completion marker.
				if err := part.Sync(); err != nil {
					setErr(err)
					return
				}
				mu.Lock()
				st.Chunks[idx].Done = true
				saveErr := d.saveState(statePath, st)
				mu.Unlock()
				if saveErr != nil {
					setErr(saveErr)
					return
				}
			}
		}()
	}

	for idx := range st.Chunks {
		if st.Chunks[idx].Done {
			continue
		}
		select {
		case jobs <- idx:
		case <-workCtx.Done():
			close(jobs)
			wg.Wait()
			if firstErr != nil {
				return firstErr
			}
			return workCtx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		if errors.Is(firstErr, errDegraded) {
			return firstErr
		}
		return firstErr
	}
	return nil
}

// finalize verifies size and digest, then atomically moves the partial file
// into place and removes the state file.
func (d *Downloader) finalize(ctx context.Context, dest, partPath, statePath string, st *downloadState) (*Result, error) {
	info, err := os.Stat(partPath)
	if err != nil {
		return nil, err
	}
	if info.Size() != st.TotalSize {
		removeIfExists(partPath)
		removeIfExists(statePath)
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrSize, info.Size(), st.TotalSize)
	}
	if d.cfg.ExpectedSize > 0 && info.Size() != d.cfg.ExpectedSize {
		removeIfExists(partPath)
		removeIfExists(statePath)
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrSize, info.Size(), d.cfg.ExpectedSize)
	}

	sum, err := hashFile(partPath)
	if err != nil {
		return nil, err
	}
	hexdigest := hex.EncodeToString(sum[:])
	if d.cfg.ExpectedSHA256 != "" && !strings.EqualFold(hexdigest, d.cfg.ExpectedSHA256) {
		removeIfExists(partPath)
		removeIfExists(statePath)
		return nil, fmt.Errorf("%w: got %s, want %s", ErrChecksum, hexdigest, d.cfg.ExpectedSHA256)
	}

	if err := os.Rename(partPath, dest); err != nil {
		return nil, fmt.Errorf("rangedl: rename into place: %w", err)
	}
	removeIfExists(statePath)
	if dir, err := os.Open(filepath.Dir(dest)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return &Result{
		Path:         dest,
		Size:         info.Size(),
		ETag:         st.ETag,
		LastModified: st.LastModified,
		SHA256:       hexdigest,
	}, nil
}

func hashFile(path string) ([sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [sha256.Size]byte{}, err
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
