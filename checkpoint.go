package lscheckpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

// PagesPerChunk is the number of pages packed into one chunk file.
const PagesPerChunk = 8

var chunkMagic = []byte("LCCK")

// PageMeta records the hash of one serialized page.
type PageMeta struct {
	ID   int    `json:"id"`
	Hash string `json:"hash"` // sha256 hex of the serialized page
}

// ChunkMeta describes one chunk file within a checkpoint.
type ChunkMeta struct {
	Index     int        `json:"index"`
	File      string     `json:"file"`
	Pages     []PageMeta `json:"pages"`
	Hash      string     `json:"hash"`      // sha256 hex of the chunk file bytes
	ChainHash string     `json:"chainHash"` // hash chain up to and including this chunk
}

// Manifest is the descriptor of one checkpoint. Incremental checkpoints
// only contain pages changed since Base.
type Manifest struct {
	Version  int         `json:"version"`
	Seq      uint64      `json:"seq"`
	Base     string      `json:"base,omitempty"` // base checkpoint dir name (incremental)
	Chunks   []ChunkMeta `json:"chunks"`
	RootHash string      `json:"rootHash"` // final hash chain value
}

// serializePage deterministically encodes a page (keys sorted).
func serializePage(p *Page) []byte {
	keys := make([]string, 0, len(p.Data))
	for k := range p.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], uint32(p.ID))
	buf.Write(tmp[:4])
	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(keys)))
	buf.Write(tmp[:4])
	for _, k := range keys {
		v := p.Data[k]
		binary.LittleEndian.PutUint32(tmp[:4], uint32(len(k)))
		buf.Write(tmp[:4])
		buf.WriteString(k)
		binary.LittleEndian.PutUint32(tmp[:4], uint32(len(v)))
		buf.Write(tmp[:4])
		buf.Write(v)
	}
	return buf.Bytes()
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// serializeChunk encodes pages into a chunk file body.
func serializeChunk(pages []*Page) []byte {
	var buf bytes.Buffer
	buf.Write(chunkMagic)
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:4], 1) // version
	buf.Write(tmp[:4])
	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(pages)))
	buf.Write(tmp[:4])
	for _, p := range pages {
		buf.Write(serializePage(p))
	}
	return buf.Bytes()
}

// chainStep folds one chunk hash into the running hash chain.
func chainStep(chain, chunkHash string) string {
	sum := sha256.Sum256([]byte(chain + chunkHash))
	return hex.EncodeToString(sum[:])
}

// Checkpointer generates checkpoints into a directory. It tracks the
// previous checkpoint to produce incremental diff packages.
type Checkpointer struct {
	Dir string

	mu        sync.Mutex
	lastDir   string
	lastPages map[int]string // pageID -> page hash of last checkpoint
}

// NewCheckpointer creates a Checkpointer writing checkpoints under dir.
func NewCheckpointer(dir string) *Checkpointer {
	return &Checkpointer{Dir: dir}
}

// hashedPage pairs a page with its content hash.
type hashedPage struct {
	page *Page
	data []byte // serialized form
	hash string
}

// hashPages serializes and hashes snapshot pages in parallel.
func hashPages(pages []*Page) []hashedPage {
	out := make([]hashedPage, len(pages))
	workers := runtime.NumCPU()
	if workers > len(pages) {
		workers = len(pages)
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				data := serializePage(pages[i])
				out[i] = hashedPage{page: pages[i], data: data, hash: hashBytes(data)}
			}
		}()
	}
	for i := range pages {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

// Checkpoint generates a checkpoint of snap. If incremental is true and a
// previous checkpoint exists, only changed pages are written (diff package).
// Page hashing and chunk writing run in parallel; the snapshot is immutable,
// so concurrent writers are never blocked.
func (c *Checkpointer) Checkpoint(snap *Snapshot, incremental bool) (*Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	name := fmt.Sprintf("ckpt-%06d", snap.seq)
	dir := filepath.Join(c.Dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	hashed := hashPages(snap.pages)

	// Diff against the previous checkpoint for incremental packages.
	changed := make([]hashedPage, 0, len(hashed))
	base := ""
	if incremental && c.lastPages != nil {
		base = filepath.Base(c.lastDir)
		for _, hp := range hashed {
			if c.lastPages[hp.page.ID] != hp.hash {
				changed = append(changed, hp)
			}
		}
	} else {
		changed = hashed
	}

	// Group changed pages into chunks and write them in parallel.
	type chunkJob struct {
		index int
		pages []hashedPage
	}
	var jobs []chunkJob
	for i := 0; i < len(changed); i += PagesPerChunk {
		end := i + PagesPerChunk
		if end > len(changed) {
			end = len(changed)
		}
		jobs = append(jobs, chunkJob{index: i / PagesPerChunk, pages: changed[i:end]})
	}
	chunks := make([]ChunkMeta, len(jobs))
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job chunkJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pages := make([]*Page, len(job.pages))
			metas := make([]PageMeta, len(job.pages))
			for j, hp := range job.pages {
				pages[j] = hp.page
				metas[j] = PageMeta{ID: hp.page.ID, Hash: hp.hash}
			}
			body := serializeChunk(pages)
			file := fmt.Sprintf("chunk-%06d.bin", job.index)
			if err := os.WriteFile(filepath.Join(dir, file), body, 0o644); err != nil {
				errs[i] = err
				return
			}
			chunks[i] = ChunkMeta{
				Index: job.index,
				File:  file,
				Pages: metas,
				Hash:  hashBytes(body),
			}
		}(i, job)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	// Hash chain over chunk hashes in order.
	chain := ""
	for i := range chunks {
		chain = chainStep(chain, chunks[i].Hash)
		chunks[i].ChainHash = chain
	}
	root := chain
	if root == "" {
		root = hashBytes(nil) // empty incremental diff
	}

	m := &Manifest{Version: 1, Seq: snap.seq, Base: base, Chunks: chunks, RootHash: root}
	mdata, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mdata, 0o644); err != nil {
		return nil, err
	}

	// Record the full page-hash map for the next incremental diff.
	lastPages := c.lastPages
	if lastPages == nil || base == "" {
		lastPages = make(map[int]string, len(hashed))
	}
	for _, hp := range hashed {
		lastPages[hp.page.ID] = hp.hash
	}
	c.lastPages = lastPages
	c.lastDir = dir
	return m, nil
}
