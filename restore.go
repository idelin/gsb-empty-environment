package lscheckpoint

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// CorruptionError pinpoints a corrupted chunk discovered during restore.
type CorruptionError struct {
	Checkpoint string // checkpoint directory
	ChunkIndex int    // index of the bad chunk within the checkpoint
	File       string // chunk file path
	Expected   string // hash recorded in the manifest
	Actual     string // hash computed from the file on disk
}

func (e *CorruptionError) Error() string {
	return fmt.Sprintf("checkpoint integrity violation: chunk %d (%s) hash mismatch: expected %s, got %s",
		e.ChunkIndex, e.File, e.Expected, e.Actual)
}

// ChainError reports a broken hash chain in a manifest.
type ChainError struct {
	Checkpoint string
	ChunkIndex int
	Expected   string
	Actual     string
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("checkpoint %s: hash chain broken at chunk %d: expected %s, got %s",
		e.Checkpoint, e.ChunkIndex, e.Expected, e.Actual)
}

// PageHashError reports a page whose content hash does not match the manifest.
type PageHashError struct {
	Checkpoint string
	ChunkIndex int
	PageID     int
	Expected   string
	Actual     string
}

func (e *PageHashError) Error() string {
	return fmt.Sprintf("checkpoint %s: page %d in chunk %d hash mismatch: expected %s, got %s",
		e.Checkpoint, e.PageID, e.ChunkIndex, e.Expected, e.Actual)
}

func loadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("checkpoint %s: invalid manifest: %w", dir, err)
	}
	return &m, nil
}

// parseChunk decodes a chunk file body into pages.
func parseChunk(body []byte) ([]*Page, error) {
	if len(body) < 12 || !bytes.Equal(body[:4], chunkMagic) {
		return nil, fmt.Errorf("bad chunk magic")
	}
	r := bytes.NewReader(body[8:]) // skip magic + version
	var pageCount uint32
	if err := binary.Read(r, binary.LittleEndian, &pageCount); err != nil {
		return nil, err
	}
	pages := make([]*Page, 0, pageCount)
	for i := uint32(0); i < pageCount; i++ {
		var pageID, entryCount uint32
		if err := binary.Read(r, binary.LittleEndian, &pageID); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &entryCount); err != nil {
			return nil, err
		}
		p := &Page{ID: int(pageID), Data: make(map[string][]byte, entryCount)}
		for j := uint32(0); j < entryCount; j++ {
			var klen uint32
			if err := binary.Read(r, binary.LittleEndian, &klen); err != nil {
				return nil, err
			}
			k := make([]byte, klen)
			if _, err := r.Read(k); err != nil {
				return nil, err
			}
			var vlen uint32
			if err := binary.Read(r, binary.LittleEndian, &vlen); err != nil {
				return nil, err
			}
			v := make([]byte, vlen)
			if _, err := r.Read(v); err != nil {
				return nil, err
			}
			p.Data[string(k)] = v
		}
		pages = append(pages, p)
	}
	return pages, nil
}

// verifyCheckpoint reads every chunk of one checkpoint, verifying the file
// hash against the manifest and the hash chain. It returns the parsed pages
// keyed by page ID. The first integrity violation aborts with a precise
// error identifying the bad chunk.
func verifyCheckpoint(dir string, m *Manifest) (map[int]*Page, error) {
	pages := make(map[int]*Page)
	chain := ""
	for _, cm := range m.Chunks {
		path := filepath.Join(dir, cm.File)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, &CorruptionError{
				Checkpoint: dir, ChunkIndex: cm.Index, File: path,
				Expected: cm.Hash, Actual: "unreadable: " + err.Error(),
			}
		}
		actual := hashBytes(body)
		if actual != cm.Hash {
			return nil, &CorruptionError{
				Checkpoint: dir, ChunkIndex: cm.Index, File: path,
				Expected: cm.Hash, Actual: actual,
			}
		}
		chain = chainStep(chain, cm.Hash)
		if chain != cm.ChainHash {
			return nil, &ChainError{
				Checkpoint: dir, ChunkIndex: cm.Index,
				Expected: cm.ChainHash, Actual: chain,
			}
		}
		parsed, err := parseChunk(body)
		if err != nil {
			return nil, &CorruptionError{
				Checkpoint: dir, ChunkIndex: cm.Index, File: path,
				Expected: cm.Hash, Actual: "undecodable: " + err.Error(),
			}
		}
		// Verify each page hash against the manifest entry.
		want := make(map[int]string, len(cm.Pages))
		for _, pm := range cm.Pages {
			want[pm.ID] = pm.Hash
		}
		for _, p := range parsed {
			if got := hashBytes(serializePage(p)); got != want[p.ID] {
				return nil, &PageHashError{
					Checkpoint: dir, ChunkIndex: cm.Index, PageID: p.ID,
					Expected: want[p.ID], Actual: got,
				}
			}
			pages[p.ID] = p
		}
	}
	if chain == "" {
		chain = hashBytes(nil)
	}
	if chain != m.RootHash {
		return nil, &ChainError{Checkpoint: dir, ChunkIndex: -1, Expected: m.RootHash, Actual: chain}
	}
	return pages, nil
}

// Restore rebuilds a StateMachine from the checkpoint stored in dir,
// walking the base chain for incremental checkpoints. Every chunk file is
// verified against the manifest hash chain; any corruption is reported
// with the exact chunk index and the restore is rejected.
func Restore(dir string) (*StateMachine, error) {
	// Collect the manifest chain from oldest base to newest.
	var chain []*Manifest
	var dirs []string
	cur := dir
	for {
		m, err := loadManifest(cur)
		if err != nil {
			return nil, err
		}
		chain = append([]*Manifest{m}, chain...)
		dirs = append([]string{cur}, dirs...)
		if m.Base == "" {
			break
		}
		cur = filepath.Join(filepath.Dir(cur), m.Base)
	}

	// Verify and apply checkpoints oldest-first so newer pages override.
	merged := make(map[int]*Page)
	for i, m := range chain {
		pages, err := verifyCheckpoint(dirs[i], m)
		if err != nil {
			return nil, err
		}
		for id, p := range pages {
			merged[id] = p
		}
	}

	sm := NewStateMachine()
	for id, p := range merged {
		if id < 0 || id >= NumPages {
			return nil, fmt.Errorf("checkpoint %s: page id %d out of range", dir, id)
		}
		sm.pages[id] = p
	}
	sm.seq = chain[len(chain)-1].Seq
	return sm, nil
}

// VerifyCheckpointDir verifies a checkpoint directory without restoring.
// It returns the sorted list of chunk files that passed verification.
func VerifyCheckpointDir(dir string) ([]string, error) {
	m, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}
	if _, err := verifyCheckpoint(dir, m); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(m.Chunks))
	for _, cm := range m.Chunks {
		files = append(files, cm.File)
	}
	sort.Strings(files)
	return files, nil
}
