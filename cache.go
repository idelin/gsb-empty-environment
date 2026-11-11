package symbolcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxFetchAttempts bounds how many full mirror sweeps are performed while
// callers are still waiting.
const maxFetchAttempts = 3

// Handle is an independently owned, read-only view of a cached PDB. Every
// caller receives its own *os.File and must Close it when finished.
type Handle struct {
	file *os.File
	size int64
	key  Key
	rel  string
}

// ReadAt implements io.ReaderAt directly on the cached file.
func (h *Handle) ReadAt(p []byte, off int64) (int, error) {
	return h.file.ReadAt(p, off)
}

// ReaderAt returns the underlying cached file as an io.ReaderAt.
func (h *Handle) ReaderAt() io.ReaderAt { return h.file }

// Size is the exact PDB length in bytes.
func (h *Handle) Size() int64 { return h.size }

// Key returns the key the handle was fetched under.
func (h *Handle) Key() Key { return h.key }

// RelativePath returns the symbol-server style path of the object.
func (h *Handle) RelativePath() string { return h.rel }

// Close releases the underlying file descriptor.
func (h *Handle) Close() error { return h.file.Close() }

// Cache stores validated PDBs on local disk. It is safe for concurrent use.
type Cache struct {
	root    string
	mirrors []Mirror

	mu       sync.Mutex
	inflight map[string]*download
}

// download is the shared, single-flight state for one index path.
type download struct {
	done chan struct{}

	// All guarded by Cache.mu.
	waiters   int
	published string // index path of the validated object, empty on failure
	err       error
	cancel    context.CancelFunc
}

// New opens (creating if needed) a cache rooted at dir, using mirrors in order
// as fallback sources. Partial files left behind by crashed or interrupted
// writers are swept away up front, so they can never be mistaken for objects.
func New(dir string, mirrors ...Mirror) (*Cache, error) {
	if len(mirrors) == 0 {
		return nil, errors.New("symbolcache: at least one mirror is required")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("symbolcache: creating cache dir: %w", err)
	}
	if err := sweepTemp(dir); err != nil {
		return nil, err
	}
	return &Cache{root: dir, mirrors: mirrors, inflight: make(map[string]*download)}, nil
}

// Get returns a read handle for the PDB identified by key. On a cache hit the
// already published file is opened directly; otherwise the object is fetched
// exactly once and shared by every request carrying the same key.
//
// Canceling ctx only affects this caller. When several requests share one
// download, the first caller keeps the network fetch running until the object
// is fully validated or every waiter has gone away.
func (c *Cache) Get(ctx context.Context, key Key) (*Handle, error) {
	idx, err := key.index()
	if err != nil {
		return nil, err
	}

	// Fast path: an object published earlier is opened without taking mu.
	if h, err := openHandle(c.root, idx, key); err == nil {
		return h, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	c.mu.Lock()
	// Re-check under the lock to cover a publish that raced the fast path.
	if h, err := openHandle(c.root, idx, key); err == nil {
		c.mu.Unlock()
		return h, nil
	}

	d, ok := c.inflight[idx]
	if !ok {
		d = &download{done: make(chan struct{}), waiters: 1}
		c.inflight[idx] = d
		c.mu.Unlock()
		go c.run(idx, key, d)
	} else {
		d.waiters++
		c.mu.Unlock()
	}

	select {
	case <-ctx.Done():
		c.abandon(d)
		return nil, ctx.Err()
	case <-d.done:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	return openHandle(c.root, d.published, key)
}

// abandon removes a caller that gave up before the shared download finished.
// When the last waiter leaves, the coordinator context is canceled so a mirror
// serving nobody is stopped.
func (c *Cache) abandon(d *download) {
	c.mu.Lock()
	d.waiters--
	cancel := d.cancel
	orphaned := d.waiters == 0
	c.mu.Unlock()
	if orphaned && cancel != nil {
		cancel()
	}
}

func (c *Cache) run(idx string, key Key, d *download) {
	ctx, cancel := context.WithCancel(context.Background())

	c.mu.Lock()
	d.cancel = cancel
	c.mu.Unlock()

	defer cancel()
	defer close(d.done)

	var lastErr error
	for attempt := 0; attempt < maxFetchAttempts; attempt++ {
		// Another process or run may have published meanwhile; adopt it.
		if h, openErr := openHandle(c.root, idx, key); openErr == nil {
			_ = h.Close()
			c.complete(d, idx, nil, idx)
			return
		}

		var err error
		_, err = c.fetchOnce(ctx, idx, key)
		if err == nil {
			c.complete(d, idx, nil, idx)
			return
		}
		lastErr = err

		c.mu.Lock()
		orphaned := d.waiters == 0
		c.mu.Unlock()
		if ctx.Err() != nil || orphaned {
			c.complete(d, "", lastErr, idx)
			return
		}
		if attempt == maxFetchAttempts-1 {
			break
		}
		if err := sleepWithContext(ctx, backoff(attempt)); err != nil {
			c.complete(d, "", err, idx)
			return
		}
	}
	c.complete(d, "", fmt.Errorf("symbolcache: all mirrors failed for %s: %w", idx, lastErr), idx)
}

func (c *Cache) complete(d *download, published string, err error, idx string) {
	c.mu.Lock()
	d.published, d.err = published, err
	delete(c.inflight, idx)
	c.mu.Unlock()
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * 50 * time.Millisecond
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
