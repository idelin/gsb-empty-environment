package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
)

// MetaFileName is an optional JSON file at a source archive root carrying
// per-path versions and tombstones. Without it every path has version 0.
const MetaFileName = ".archmerge.json"

type sourceMeta struct {
	Versions   map[string]int64 `json:"versions"`
	Tombstones []struct {
		Path    string `json:"path"`
		Version int64  `json:"version"`
	} `json:"tombstones"`
}

// LoadSource scans a source archive directory. It only reads; the archive
// is never modified. Directories (including empty ones) become dir entries,
// files become file entries with a sha256 content digest.
func LoadSource(name, root string) (Source, error) {
	src := Source{Name: name}
	var meta sourceMeta
	b, err := ioutil.ReadFile(filepath.Join(root, MetaFileName))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &meta); err != nil {
			return src, fmt.Errorf("%s: invalid %s: %v", name, MetaFileName, err)
		}
	case !os.IsNotExist(err):
		return src, err
	}

	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			src.Entries = append(src.Entries, Entry{
				Path: rel, Type: TypeDir, Version: meta.Versions[rel],
			})
			return nil
		}
		if rel == MetaFileName {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		src.Entries = append(src.Entries, Entry{
			Path:    rel,
			Type:    TypeFile,
			Version: meta.Versions[rel],
			Digest:  "sha256:" + hex.EncodeToString(h.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return src, err
	}

	for _, t := range meta.Tombstones {
		src.Entries = append(src.Entries, Entry{
			Path: t.Path, Type: TypeTombstone, Version: t.Version,
		})
	}
	sort.Slice(src.Entries, func(i, j int) bool {
		return src.Entries[i].Path < src.Entries[j].Path
	})
	return src, nil
}
