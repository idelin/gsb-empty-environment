package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Meta describes a generated checkpoint file.
type Meta struct {
	ID          uint64   // checkpoint sequence number
	Version     uint64   // store version covered by this checkpoint
	BaseVersion uint64   // store version covered by the previous checkpoint
	Path        string   // file path
	NumPages    int      // dirty pages included (delta size)
	NumChunks   int      // chunks in the file
	PrevChain   [32]byte // chain hash of the previous checkpoint
	ChainHash   [32]byte // final chain hash of this checkpoint
}

// Checkpointer generates incremental checkpoints of a Store.
type Checkpointer struct {
	store *Store
	dir   string

	mu          sync.Mutex
	nextID      uint64
	lastVersion uint64
	lastChain   [32]byte
}

// NewCheckpointer creates a Checkpointer writing files into dir.
func NewCheckpointer(store *Store, dir string) (*Checkpointer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Checkpointer{store: store, dir: dir}, nil
}

type dirtyPage struct {
	id   int
	page *page
}

// Checkpoint takes a consistent snapshot of the store and writes an
// incremental checkpoint containing only pages modified since the previous
// checkpoint. Chunk serialization and hashing run in parallel; the hash
// chain is then folded sequentially to keep it deterministic.
func (c *Checkpointer) Checkpoint() (*Meta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := c.store.BeginSnapshot()
	defer snap.Release()

	var dirty []dirtyPage
	for i, p := range snap.pages {
		if p.version > c.lastVersion {
			dirty = append(dirty, dirtyPage{i, p})
		}
	}

	var chunks [][]dirtyPage
	for i := 0; i < len(dirty); i += chunkPages {
		end := i + chunkPages
		if end > len(dirty) {
			end = len(dirty)
		}
		chunks = append(chunks, dirty[i:end])
	}

	// Parallel serialize + hash per chunk.
	type chunkResult struct {
		data []byte
		hash [32]byte
	}
	results := make([]chunkResult, len(chunks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, ch := range chunks {
		wg.Add(1)
		go func(i int, ch []dirtyPage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data := encodeChunk(ch)
			results[i] = chunkResult{data: data, hash: sha256.Sum256(data)}
		}(i, ch)
	}
	wg.Wait()

	// Sequential hash chain over ordered chunk hashes.
	chain := c.lastChain
	for _, r := range results {
		chain = sha256.Sum256(append(chain[:], r.hash[:]...))
	}

	id := c.nextID
	path := filepath.Join(c.dir, fmt.Sprintf("ckpt-%06d.ckpt", id))

	buf := &bytes.Buffer{}
	buf.WriteString(magic)
	writeU64(buf, id)
	writeU64(buf, snap.version)
	buf.Write(c.lastChain[:])
	writeU32(buf, uint32(len(chunks)))
	for _, r := range results {
		writeU32(buf, uint32(len(r.data)))
		buf.Write(r.hash[:])
		buf.Write(r.data)
	}
	writeU32(buf, uint32(len(dirty)))
	for i, ch := range chunks {
		for _, dp := range ch {
			writeU32(buf, uint32(dp.id))
			writeU64(buf, dp.page.version)
			writeU32(buf, uint32(i))
		}
	}
	buf.Write(chain[:])

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	meta := &Meta{
		ID:          id,
		Version:     snap.version,
		BaseVersion: c.lastVersion,
		Path:        path,
		NumPages:    len(dirty),
		NumChunks:   len(chunks),
		PrevChain:   c.lastChain,
		ChainHash:   chain,
	}
	c.nextID++
	c.lastVersion = snap.version
	c.lastChain = chain
	return meta, nil
}
