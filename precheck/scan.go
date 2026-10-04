package precheck

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// ScanDir walks a real directory tree and returns its entries with
// slash-separated paths relative to root.
func ScanDir(root string) ([]Entry, error) {
	var entries []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		typ := File
		if d.IsDir() {
			typ = Dir
		}
		entries = append(entries, Entry{Path: filepath.ToSlash(rel), Type: typ})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// ParseList reads entries from a text stream, one per line:
//
//	F <path>   (file)
//	D <path>   (directory)
//
// Blank lines and lines starting with '#' are ignored. The path is taken
// verbatim after the type token so that significant trailing spaces survive.
func ParseList(r io.Reader) ([]Entry, error) {
	var entries []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(trimmed) < 2 || (trimmed[1] != ' ' && trimmed[1] != '\t') {
			return nil, fmt.Errorf("line %d: expected 'F <path>' or 'D <path>'", lineNo)
		}
		var typ EntryType
		switch trimmed[0] {
		case 'F', 'f':
			typ = File
		case 'D', 'd':
			typ = Dir
		default:
			return nil, fmt.Errorf("line %d: unknown entry type %q", lineNo, trimmed[0])
		}
		path := strings.TrimLeft(trimmed[2:], " \t")
		if path == "" {
			return nil, fmt.Errorf("line %d: empty path", lineNo)
		}
		entries = append(entries, Entry{Path: path, Type: typ})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
