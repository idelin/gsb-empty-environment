package pkgincr

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ManifestEntry records the metadata of one packaged path so a later run
// can tell whether it changed.
type ManifestEntry struct {
	IsDir      bool   `json:"isDir,omitempty"`
	IsSymlink  bool   `json:"isSymlink,omitempty"`
	LinkTarget string `json:"linkTarget,omitempty"`
	Size       int64  `json:"size"`
	ModTimeNS  int64  `json:"modTimeNS"`
	Mode       uint32 `json:"mode"`
}

// Manifest maps root-relative paths to their packaged state.
type Manifest struct {
	Entries map[string]ManifestEntry `json:"entries"`
}

// LoadManifest reads a manifest file. A missing file yields an empty
// manifest (first run).
func LoadManifest(name string) (*Manifest, error) {
	m := &Manifest{Entries: map[string]ManifestEntry{}}
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("manifest %s: %v", name, err)
	}
	if m.Entries == nil {
		m.Entries = map[string]ManifestEntry{}
	}
	return m, nil
}

// Save writes the manifest atomically.
func (m *Manifest) Save(name string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// manifestEntry converts a walk entry to its manifest record.
func manifestEntry(e *Entry) ManifestEntry {
	return ManifestEntry{
		IsDir:      e.IsDir,
		IsSymlink:  e.IsSymlink,
		LinkTarget: e.LinkTarget,
		Size:       e.Size,
		ModTimeNS:  e.ModTime.UnixNano(),
		Mode:       uint32(e.Mode),
	}
}

// PackStats summarizes a Pack run.
type PackStats struct {
	Written   int      // entries written to the archive
	Unchanged int      // entries skipped because the manifest says they are identical
	Removed   []string // paths present in the previous manifest but gone now
}

// Pack writes the included entries of res to out as a tar archive and
// returns the manifest describing everything that is now considered
// packaged.
//
// Incremental behaviour: regular files and symlinks whose metadata is
// unchanged with respect to prev are not written again (their bytes are
// already contained in earlier archives of the chain). Directory entries
// carry no data and are always written so empty directories survive.
// Symlinks are stored as links only; their targets are never read.
func Pack(root string, res *Result, out io.Writer, prev *Manifest) (*Manifest, PackStats, error) {
	var stats PackStats
	next := &Manifest{Entries: map[string]ManifestEntry{}}
	tw := tar.NewWriter(out)
	for _, e := range res.Included() {
		me := manifestEntry(e)
		next.Entries[e.Path] = me
		if !e.IsDir && prev != nil {
			if old, ok := prev.Entries[e.Path]; ok && old == me {
				stats.Unchanged++
				continue
			}
		}
		if err := writeTarEntry(root, tw, e); err != nil {
			return nil, stats, err
		}
		stats.Written++
	}
	if err := tw.Close(); err != nil {
		return nil, stats, err
	}
	if prev != nil {
		for p := range prev.Entries {
			if _, ok := next.Entries[p]; !ok {
				stats.Removed = append(stats.Removed, p)
			}
		}
		sort.Strings(stats.Removed)
	}
	return next, stats, nil
}

// writeTarEntry appends one entry to the archive.
func writeTarEntry(root string, tw *tar.Writer, e *Entry) error {
	hdr := &tar.Header{
		Name:    e.Path,
		ModTime: e.ModTime,
		Mode:    int64(e.Mode.Perm()),
	}
	switch {
	case e.IsDir:
		hdr.Typeflag = tar.TypeDir
		hdr.Name += "/"
	case e.IsSymlink:
		// Only the link itself is stored; the target is never followed.
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = e.LinkTarget
	default:
		hdr.Typeflag = tar.TypeReg
		hdr.Size = e.Size
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(e.Path)))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}
