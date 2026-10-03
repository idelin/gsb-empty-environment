package checkpoint

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// CorruptionError reports an integrity failure detected during recovery.
// ChunkIndex is the 0-based index of the corrupted chunk, or -1 when the
// failure is in the header/manifest/hash-chain rather than a single chunk.
type CorruptionError struct {
	File       string
	ChunkIndex int
	PageIDs    []uint32
	Detail     string
}

func (e *CorruptionError) Error() string {
	if e.ChunkIndex >= 0 {
		return fmt.Sprintf("checkpoint %s: chunk #%d corrupted, affected pages %v: %s",
			e.File, e.ChunkIndex+1, e.PageIDs, e.Detail)
	}
	return fmt.Sprintf("checkpoint %s: %s", e.File, e.Detail)
}

// RecoveredState is the verified state rebuilt from checkpoint files.
type RecoveredState struct {
	Version uint64
	KV      map[string]string
	Chain   [32]byte
}

// Recover verifies and applies an ordered chain of checkpoint files
// (base checkpoint first, then incremental deltas). Every chunk hash and
// the cross-file hash chain are verified; any mismatch aborts recovery
// with a CorruptionError that pinpoints the bad chunk and its pages.
func Recover(paths []string) (*RecoveredState, error) {
	pages := make(map[uint32]decodedPage)
	var prevChain [32]byte
	var version uint64

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p, err := parseCheckpoint(raw)
		if err != nil {
			return nil, &CorruptionError{File: path, ChunkIndex: -1, Detail: "malformed file: " + err.Error()}
		}
		if p.prevChain != prevChain {
			return nil, &CorruptionError{
				File:       path,
				ChunkIndex: -1,
				Detail:     "hash chain linkage broken: prevChain does not match the previous checkpoint",
			}
		}

		chain := prevChain
		for i, ch := range p.chunks {
			sum := sha256.Sum256(ch.data)
			if sum != ch.hash {
				return nil, &CorruptionError{
					File:       path,
					ChunkIndex: i,
					PageIDs:    pagesInChunk(p.manifest, uint32(i)),
					Detail:     "chunk hash mismatch",
				}
			}
			chain = sha256.Sum256(append(chain[:], ch.hash[:]...))
		}
		if chain != p.finalChain {
			return nil, &CorruptionError{
				File:       path,
				ChunkIndex: -1,
				Detail:     "final hash chain mismatch",
			}
		}

		for _, ch := range p.chunks {
			decoded, err := decodeChunk(ch.data)
			if err != nil {
				return nil, &CorruptionError{File: path, ChunkIndex: -1, Detail: "undecodable chunk: " + err.Error()}
			}
			for _, dp := range decoded {
				if cur, ok := pages[dp.id]; !ok || dp.version > cur.version {
					pages[dp.id] = dp
				}
			}
		}
		prevChain = chain
		version = p.version
	}

	state := &RecoveredState{Version: version, KV: make(map[string]string), Chain: prevChain}
	for _, dp := range pages {
		for k, v := range dp.entries {
			state.KV[k] = v
		}
	}
	return state, nil
}

// RecoverDir verifies and applies every checkpoint file in dir, in order.
func RecoverDir(dir string) (*RecoveredState, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ckpt") {
			paths = append(paths, dir+string(os.PathSeparator)+e.Name())
		}
	}
	return Recover(paths)
}

func pagesInChunk(manifest []manifestEntry, chunkIdx uint32) []uint32 {
	var ids []uint32
	for _, e := range manifest {
		if e.chunkIdx == chunkIdx {
			ids = append(ids, e.pageID)
		}
	}
	return ids
}
