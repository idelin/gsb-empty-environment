package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// buffer accumulates chunks of one (version, name) pair until the
// declared size is reached. Chunks are keyed by manifest version, so
// bytes that arrived under an old manifest can never be spliced into
// a file pinned by a newer manifest.
type buffer struct {
	chunks   map[int64][]byte
	received int64
}

func (b *buffer) add(offset int64, data []byte) {
	if _, dup := b.chunks[offset]; dup {
		return // idempotent retransmission
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	b.chunks[offset] = cp
	b.received += int64(len(data))
}

// assemble returns the contiguous image starting at offset 0, or nil.
func (b *buffer) assemble(size int64) []byte {
	if b.received != size {
		return nil
	}
	out := make([]byte, 0, size)
	var pos int64
	for pos < size {
		chunk, ok := b.chunks[pos]
		if !ok {
			return nil
		}
		out = append(out, chunk...)
		pos += int64(len(chunk))
	}
	return out
}

// store holds sealed (fully received and digest-verified) files keyed
// by manifest version + name, plus in-flight chunk buffers. When dir
// is non-empty, sealed files are persisted atomically (tmp + rename)
// so a verifier restarted on the same dir recovers them.
type store struct {
	dir     string
	mu      sync.Mutex
	sealed  map[string][]byte
	buffers map[string]*buffer
}

func storeKey(version, name string) string { return version + "\x00" + name }

func openStore(dir string) (*store, error) {
	s := &store{
		dir:     dir,
		sealed:  map[string][]byte{},
		buffers: map[string]*buffer{},
	}
	if dir == "" {
		return s, nil
	}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasSuffix(info.Name(), ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) != 2 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s.sealed[storeKey(parts[0], parts[1])] = data
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *store) bufferFor(version, name string) *buffer {
	k := storeKey(version, name)
	b, ok := s.buffers[k]
	if !ok {
		b = &buffer{chunks: map[int64][]byte{}}
		s.buffers[k] = b
	}
	return b
}

// seal atomically commits a fully verified file: it becomes visible in
// the in-memory map and, if a dir is configured, on disk via rename.
func (s *store) seal(version, name string, data []byte) error {
	k := storeKey(version, name)
	s.sealed[k] = data
	delete(s.buffers, k)
	if s.dir == "" {
		return nil
	}
	final := filepath.Join(s.dir, version, name)
	if err := os.MkdirAll(filepath.Dir(final), 0755); err != nil {
		return err
	}
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (s *store) drop(version, name string) {
	delete(s.buffers, storeKey(version, name))
}

func (s *store) get(version, name string) ([]byte, bool) {
	data, ok := s.sealed[storeKey(version, name)]
	return data, ok
}
