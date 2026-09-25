package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	manifestName = "manifest.json"
	indexName    = "index.json"
	journalName  = "repair-journal.json"
	segmentsDir  = "segments"
)

type SegmentMeta struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type IndexMeta struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version  int           `json:"version"`
	Segments []SegmentMeta `json:"segments"`
	Index    *IndexMeta    `json:"index,omitempty"`
}

type IndexEntry struct {
	Segment string `json:"segment"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
}

type Index struct {
	Version int                   `json:"version"`
	Entries map[string]IndexEntry `json:"entries"`
}

func manifestPath(dir string) string { return filepath.Join(dir, manifestName) }
func indexPath(dir string) string    { return filepath.Join(dir, indexName) }
func journalPath(dir string) string  { return filepath.Join(dir, journalName) }

func segmentPath(dir, name string) string {
	return filepath.Join(dir, segmentsDir, filepath.Base(name))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

func listSegmentFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, segmentsDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// StateSummary folds manifest, index (or its absence) and every file under
// segments/ into one deterministic digest of the whole store.
func StateSummary(dir string) (string, error) {
	h := sha256.New()
	add := func(rel string, data []byte) {
		fmt.Fprintf(h, "%s\x00%d\x00%s\x00", rel, len(data), sha256Hex(data))
	}
	mb, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return "", fmt.Errorf("state summary: %w", err)
	}
	add(manifestName, mb)
	if ib, err := os.ReadFile(indexPath(dir)); err == nil {
		add(indexName, ib)
	} else if os.IsNotExist(err) {
		add(indexName+":absent", nil)
	} else {
		return "", err
	}
	segs, err := listSegmentFiles(dir)
	if err != nil {
		return "", err
	}
	for _, name := range segs {
		data, err := os.ReadFile(segmentPath(dir, name))
		if err != nil {
			return "", err
		}
		add(segmentsDir+"/"+name, data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeFileAtomic writes data to final via tmp file, fsync, rename, dir fsync.
// preRename, when non-nil, runs after the tmp file is durable but before the
// rename; it exists so fault injection can land in that window.
func writeFileAtomic(final string, data []byte, preRename func()) error {
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if preRename != nil {
		preRename()
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDir(filepath.Dir(final))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
