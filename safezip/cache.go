package safezip

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FileCache commits verified members to a directory. Writes go to a
// temporary file in the same directory and are renamed into place only
// after the full content has been fsynced, so a crash or a failed member
// can never leave a partial cache entry.
type FileCache struct {
	Dir string
	// Mode is used for the final cache file. Zero defaults to 0o600.
	Mode os.FileMode
}

func (c *FileCache) pathFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:]) + ".bin"
	return filepath.Join(c.Dir, name)
}

// Commit implements Sink. It fails atomically: either the complete file is
// renamed into place or no target file is created or modified.
func (c *FileCache) Commit(key string, content io.Reader) error {
	if c.Dir == "" {
		return ErrFormat
	}
	mode := c.Mode
	if mode == 0 {
		mode = 0o600
	}
	final := c.pathFor(key)

	tmp, err := os.CreateTemp(c.Dir, ".safezip-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := io.Copy(tmp, content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Open returns the cached content for key together with its size, or
// os.ErrNotExist when nothing has been committed yet.
func (c *FileCache) Open(key string) (io.ReadCloser, int64, error) {
	f, err := os.Open(c.pathFor(key))
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// MemCache is an in-memory Sink useful for tests and short-lived processes.
// A failed Commit leaves previously stored content untouched.
type MemCache struct {
	mu      sync.RWMutex
	content map[string][]byte
}

// NewMemCache creates an empty memory cache.
func NewMemCache() *MemCache {
	return &MemCache{content: make(map[string][]byte)}
}

func (c *MemCache) Commit(key string, content io.Reader) error {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, content); err != nil {
		return err
	}
	c.mu.Lock()
	c.content[key] = buf.Bytes()
	c.mu.Unlock()
	return nil
}

// Get returns a copy of the cached content for key.
func (c *MemCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.content[key]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// CacheMember fetches and verifies m, then commits it to sink. It is a
// thin wrapper over Fetch kept for call-site readability.
func (a *Archive) CacheMember(m *Member, sink Sink) (int64, error) {
	return a.Fetch(m, sink)
}
