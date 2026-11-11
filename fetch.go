package symbolcache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// fetchOnce walks the configured mirrors in order. The first mirror that
// delivers a fully validated object wins; partial or bogus output is discarded
// before the next mirror is tried.
func (c *Cache) fetchOnce(ctx context.Context, idx string, key Key) (*Handle, error) {
	var errs []error
	for _, mirror := range c.mirrors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := c.tryMirror(ctx, mirror, idx, key)
		if err == nil {
			return h, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// tryMirror downloads from one mirror into a private temp file, validates the
// complete MSF container and PDB identity, and only then atomically publishes
// it. Any failure leaves no trace in the object directory.
func (c *Cache) tryMirror(ctx context.Context, mirror Mirror, idx string, key Key) (_ *Handle, errOut error) {
	tmp, err := os.CreateTemp(filepath.Join(c.root, "tmp"), "fetch-*.part")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	closeOnce := func() {
		if !closed {
			_ = tmp.Close()
			closed = true
		}
	}
	var watchWG sync.WaitGroup
	// stopWatch releases the watchdog; closing it on every return path means a
	// successful publish can never have its freshly opened descriptor closed by
	// a stale watcher. The WaitGroup ensures it has fully exited first.
	stopWatch := make(chan struct{})
	watchWG.Add(1)
	go func() {
		defer watchWG.Done()
		select {
		case <-ctx.Done():
			closeOnce()
		case <-stopWatch:
		}
	}()
	defer func() {
		close(stopWatch)
		watchWG.Wait()
		closeOnce()
		// After a successful rename the path no longer exists, so this is a no-op.
		_ = os.Remove(tmpName)
	}()

	fetchErr := mirror.FetchPDB(ctx, idx, tmp)
	if fetchErr != nil {
		return nil, fmt.Errorf("download from %T: %w", mirror, fetchErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info, err := tmp.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat temp file: %w", err)
	}
	if err := validatePDB(tmp, info.Size(), key); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, fmt.Errorf("sync temp file: %w", err)
	}
	closeOnce()

	finalPath := filepath.Join(c.root, filepath.FromSlash(idx))
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return nil, fmt.Errorf("create object dir: %w", err)
	}
	// Same-directory atomic publish: readers can only observe a complete file.
	if err := os.Rename(tmpName, finalPath); err != nil {
		return nil, fmt.Errorf("publish object: %w", err)
	}

	h, err := openHandle(c.root, idx, key)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// openHandle opens a published object. Missing files return os.ErrNotExist.
func openHandle(root, idx string, key Key) (*Handle, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(idx)))
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Handle{file: f, size: info.Size(), key: key, rel: idx}, nil
}

// sweepTemp removes partial downloads left by interrupted or crashed writers.
func sweepTemp(root string) error {
	tmpDir := filepath.Join(root, "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("symbolcache: reading temp dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(tmpDir, entry.Name())); err != nil {
			return fmt.Errorf("symbolcache: removing stale temp file: %w", err)
		}
	}
	return nil
}
