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

// loadSource reads a source archive. A directory is scanned into entries
// (file digest = sha256 of content, version = mtime); any other file is
// parsed as a JSON source manifest. Inputs are only ever read.
func loadSource(arg string) (Source, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return Source{}, err
	}
	if info.IsDir() {
		return scanDir(arg, filepath.Base(arg))
	}
	data, err := ioutil.ReadFile(arg)
	if err != nil {
		return Source{}, err
	}
	var src Source
	if err := json.Unmarshal(data, &src); err != nil {
		return Source{}, fmt.Errorf("%s: %v", arg, err)
	}
	if src.Name == "" {
		src.Name = filepath.Base(arg)
	}
	if err := validateSource(&src); err != nil {
		return Source{}, err
	}
	return src, nil
}

// validateSource normalizes paths and enforces entry invariants.
func validateSource(src *Source) error {
	seen := make(map[string]bool)
	for i := range src.Entries {
		e := &src.Entries[i]
		p, err := cleanPath(e.Path)
		if err != nil {
			return fmt.Errorf("source %q: %v", src.Name, err)
		}
		e.Path = p
		switch e.Type {
		case TypeFile:
			if e.Digest == "" {
				return fmt.Errorf("source %q: file %q has no digest", src.Name, p)
			}
		case TypeDir, TypeTombstone:
			if e.Digest != "" {
				return fmt.Errorf("source %q: %s %q must not have a digest", src.Name, e.Type, p)
			}
		default:
			return fmt.Errorf("source %q: unknown entry type %q", src.Name, e.Type)
		}
		if e.Version < 0 {
			return fmt.Errorf("source %q: entry %q has negative version", src.Name, p)
		}
		if seen[p] {
			return fmt.Errorf("source %q: duplicate path %q", src.Name, p)
		}
		seen[p] = true
	}
	sort.Slice(src.Entries, func(i, j int) bool { return src.Entries[i].Path < src.Entries[j].Path })
	return nil
}

// scanDir builds a source from a real directory. Directories (including
// empty ones) become explicit dir entries; regular files become file
// entries with a sha256 digest and mtime version.
func scanDir(root, name string) (Source, error) {
	src := Source{Name: name}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
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
		switch {
		case info.IsDir():
			src.Entries = append(src.Entries, Entry{
				Path: rel, Type: TypeDir, Version: info.ModTime().Unix(),
			})
		case info.Mode().IsRegular():
			digest, err := hashFile(p)
			if err != nil {
				return err
			}
			src.Entries = append(src.Entries, Entry{
				Path: rel, Type: TypeFile, Version: info.ModTime().Unix(), Digest: digest,
			})
		}
		return nil
	})
	if err != nil {
		return Source{}, err
	}
	return src, validateSource(&src)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
