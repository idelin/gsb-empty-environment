package chunkfetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// bitmap is the recovery bitmap: bit i records that chunk i was fetched and
// passed its per-chunk digest check.
type bitmap struct {
	bits []uint64
}

func newBitmap(n int) *bitmap {
	return &bitmap{bits: make([]uint64, (n+63)/64)}
}

func (b *bitmap) Get(i int) bool {
	return b.bits[i/64]&(1<<uint(i%64)) != 0
}

func (b *bitmap) Set(i int) {
	b.bits[i/64] |= 1 << uint(i%64)
}

func (b *bitmap) Clear(i int) {
	b.bits[i/64] &^= 1 << uint(i%64)
}

// progressFile is the on-disk serialization of the recovery bitmap.
type progressFile struct {
	Version   string `json:"version"`
	Chunks    int    `json:"chunks"`
	BitmapHex string `json:"bitmap_hex"`
	UpdatedAt string `json:"updated_at"`
}

type markerFile struct {
	Version    string `json:"version"`
	Size       int64  `json:"size"`
	WholeHex   string `json:"whole_sha256"`
	RecordedAt string `json:"recorded_at"`
}

// markerState describes the existing COMPLETE marker relative to the manifest.
type markerState int

const (
	markerAbsent markerState = iota
	markerValid
	markerCorrupt
	markerVersionMismatch
)

func (f *Fetcher) chunksDir(version string) string {
	// Hex-encoding the version makes the path safe for any version string.
	return filepath.Join(f.cfg.Dir, dirChunks, hex.EncodeToString([]byte(version)))
}

func (f *Fetcher) partPath(i int) string {
	// The version directory guarantees an old-version part can never be
	// assembled into a new version, even if a stale file survives.
	return filepath.Join(f.chunksDir(f.latestVersion), fmt.Sprintf("chunk-%06d.part", i))
}

func (f *Fetcher) progressPath() string { return filepath.Join(f.cfg.Dir, fileProgress) }
func (f *Fetcher) markerPath() string   { return filepath.Join(f.cfg.Dir, fileMarker) }
func (f *Fetcher) outputPath() string   { return filepath.Join(f.cfg.Dir, fileOutput) }

// wipe removes every artifact of any previous run.
func (f *Fetcher) wipe() error {
	names := []string{
		filepath.Join(f.cfg.Dir, dirChunks),
		f.progressPath(),
		f.markerPath(),
		f.outputPath(),
	}
	for _, n := range names {
		if err := os.RemoveAll(n); err != nil {
			return err
		}
	}
	f.latestVersion = ""
	return nil
}

func (f *Fetcher) loadOrInitBitmap(man *Manifest) (*bitmap, error) {
	bm := newBitmap(man.ChunkCount())
	raw, err := os.ReadFile(f.progressPath())
	if errors.Is(err, os.ErrNotExist) {
		f.latestVersion = man.Version
		return bm, nil
	}
	if err != nil {
		return nil, err
	}
	var pf progressFile
	if jerr := json.Unmarshal(raw, &pf); jerr != nil ||
		pf.Version == "" || pf.Chunks != man.ChunkCount() {
		f.cfg.Logger("progress unreadable or incompatible; starting fresh")
		if werr := f.wipe(); werr != nil {
			return nil, werr
		}
		f.latestVersion = man.Version
		return bm, nil
	}
	if pf.Version != man.Version {
		// Source moved to a new version: discard old progress. Old chunk parts
		// live under a different version directory and are removed as well.
		f.cfg.Logger("progress is from version " + pf.Version + "; wiping old progress")
		if werr := f.wipe(); werr != nil {
			return nil, werr
		}
		f.latestVersion = man.Version
		return bm, nil
	}
	packed, err := hex.DecodeString(pf.BitmapHex)
	if err != nil {
		return nil, fmt.Errorf("chunkfetch: corrupt progress bitmap: %w", err)
	}
	if len(packed) != len(bm.bits)*8 {
		return nil, errors.New("chunkfetch: progress bitmap length mismatch")
	}
	for i := range bm.bits {
		bm.bits[i] = uint64(packed[i*8+0])<<56 | uint64(packed[i*8+1])<<48 |
			uint64(packed[i*8+2])<<40 | uint64(packed[i*8+3])<<32 |
			uint64(packed[i*8+4])<<24 | uint64(packed[i*8+5])<<16 |
			uint64(packed[i*8+6])<<8 | uint64(packed[i*8+7])
	}
	f.latestVersion = man.Version
	if err := os.MkdirAll(f.chunksDir(man.Version), 0o755); err != nil {
		return nil, err
	}
	return bm, nil
}

func (f *Fetcher) saveProgress(man *Manifest, bm *bitmap) error {
	packed := make([]byte, len(bm.bits)*8)
	for i, w := range bm.bits {
		for j := 0; j < 8; j++ {
			packed[i*8+j] = byte(w >> (56 - 8*j))
		}
	}
	pf := progressFile{
		Version:   man.Version,
		Chunks:    man.ChunkCount(),
		BitmapHex: hex.EncodeToString(packed),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return f.writeJSONAtomic(f.progressPath(), pf)
}

func (f *Fetcher) writeMarker(man *Manifest) error {
	mf := markerFile{
		Version:    man.Version,
		Size:       man.Size,
		WholeHex:   hex.EncodeToString(man.WholeSHA256),
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return f.writeJSONAtomic(f.markerPath(), mf)
}

// markerState returns markerValid only when the marker matches the manifest AND
// the assembled output file still hashes to the manifest's whole digest.
func readMarkerState(dir string, man *Manifest) markerState {
	raw, err := os.ReadFile(filepath.Join(dir, fileMarker))
	if errors.Is(err, os.ErrNotExist) {
		return markerAbsent
	}
	if err != nil {
		return markerCorrupt
	}
	var mf markerFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		return markerCorrupt
	}
	if mf.Version != man.Version || mf.WholeHex != hex.EncodeToString(man.WholeSHA256) ||
		mf.Size != man.Size {
		return markerVersionMismatch
	}
	out, err := os.Open(filepath.Join(dir, fileOutput))
	if err != nil {
		return markerCorrupt
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(h, out); err != nil {
		return markerCorrupt
	}
	if !bytes.Equal(h.Sum(nil), man.WholeSHA256) {
		return markerCorrupt
	}
	return markerValid
}

func (f *Fetcher) chunkPartGood(i int, man *Manifest) (bool, error) {
	p, err := os.Open(f.partPath(i))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer p.Close()
	h := sha256.New()
	if _, err := io.Copy(h, p); err != nil {
		return false, err
	}
	return bytes.Equal(h.Sum(nil), man.ChunkSHA256[i]), nil
}

func (f *Fetcher) removePart(i int) error {
	err := os.Remove(f.partPath(i))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// writePartAtomic writes a verified chunk via temp-file + rename, so a crash
// never leaves a half-written part that later looks complete.
func (f *Fetcher) writePartAtomic(i int, data []byte) error {
	dir := f.chunksDir(f.latestVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, f.partPath(i))
}

func (f *Fetcher) writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(tmp.Name(), path)
}
