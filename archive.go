// Package main implements a minimal, safety-first archive format ("SAR1")
// using only the Go standard library.
//
// Format (all integers little-endian):
//
//	magic   : 8 bytes  "SAR1\x00\x00\x00\x01"
//	count   : uint32   number of entries (<= MaxEntries)
//	per entry:
//	  pathLen : uint16  length of path in bytes (1..MaxPathLen)
//	  path    : []byte  UTF-8, '/'-separated relative path
//	  size    : uint64  content length in bytes (<= MaxFileSize)
//	  digest  : 32 bytes SHA-256 of content
//	  content : size bytes
//
// Decoding never executes host code: the format is a plain length-prefixed
// byte layout parsed with explicit bounds checks. Every length field is
// validated against a hard cap BEFORE any allocation is made.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

var magic = [8]byte{'S', 'A', 'R', '1', 0, 0, 0, 1}

// Hard resource caps. Checked before allocation during decoding.
const (
	MaxEntries  = 1 << 16 // 65536 entries
	MaxPathLen  = 1024    // bytes per path
	MaxFileSize = 1 << 30 // 1 GiB per file
	MaxTotal    = 4 << 30 // 4 GiB aggregate
)

// Entry is a single regular file in the archive.
type Entry struct {
	Path    string // normalized, relative, '/'-separated
	Size    uint64
	Digest  [sha256.Size]byte
	Content []byte
}

// NormalizePath converts a raw archive path into a canonical relative form
// and rejects anything that could escape the extraction root.
func NormalizePath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if len(p) > MaxPathLen {
		return "", fmt.Errorf("path too long: %d bytes", len(p))
	}
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("path is not valid UTF-8: %q", p)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path contains NUL: %q", p)
	}
	if strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("backslash not allowed in path: %q", p)
	}
	if path.IsAbs(p) {
		return "", fmt.Errorf("absolute path rejected: %q", p)
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes root: %q", p)
	}
	return clean, nil
}

// Encode writes entries to w in SAR1 format. Paths are normalized and
// duplicate paths are rejected before anything is written.
func Encode(w io.Writer, entries []Entry) error {
	if len(entries) > MaxEntries {
		return fmt.Errorf("too many entries: %d", len(entries))
	}
	seen := make(map[string]struct{}, len(entries))
	for i := range entries {
		np, err := NormalizePath(entries[i].Path)
		if err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		if _, dup := seen[np]; dup {
			return fmt.Errorf("duplicate path: %q", np)
		}
		seen[np] = struct{}{}
		entries[i].Path = np
		if uint64(len(entries[i].Content)) != entries[i].Size {
			return fmt.Errorf("entry %q: size field %d != content length %d",
				np, entries[i].Size, len(entries[i].Content))
		}
		sum := sha256.Sum256(entries[i].Content)
		if sum != entries[i].Digest {
			return fmt.Errorf("entry %q: digest mismatch", np)
		}
	}

	if _, err := w.Write(magic[:]); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(entries))); err != nil {
		return err
	}
	for _, e := range entries {
		if err := binary.Write(w, binary.LittleEndian, uint16(len(e.Path))); err != nil {
			return err
		}
		if _, err := io.WriteString(w, e.Path); err != nil {
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, e.Size); err != nil {
			return err
		}
		if _, err := w.Write(e.Digest[:]); err != nil {
			return err
		}
		if _, err := w.Write(e.Content); err != nil {
			return err
		}
	}
	return nil
}

// Decode reads and fully verifies a SAR1 archive. All length fields are
// validated against caps before allocation; each entry's size and SHA-256
// digest are verified; paths are normalized and duplicates rejected.
func Decode(r io.Reader) ([]Entry, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if hdr != magic {
		return nil, errors.New("bad magic: not a SAR1 archive")
	}

	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("reading entry count: %w", err)
	}
	if count > MaxEntries {
		return nil, fmt.Errorf("entry count %d exceeds cap %d", count, MaxEntries)
	}

	entries := make([]Entry, 0, min(count, 1024))
	seen := make(map[string]struct{}, min(count, 1024))
	var total uint64

	for i := uint32(0); i < count; i++ {
		var pathLen uint16
		if err := binary.Read(r, binary.LittleEndian, &pathLen); err != nil {
			return nil, fmt.Errorf("entry %d: reading path length: %w", i, err)
		}
		if pathLen == 0 || pathLen > MaxPathLen {
			return nil, fmt.Errorf("entry %d: path length %d out of range", i, pathLen)
		}
		// Length validated above; allocation is bounded by MaxPathLen.
		raw := make([]byte, pathLen)
		if _, err := io.ReadFull(r, raw); err != nil {
			return nil, fmt.Errorf("entry %d: reading path: %w", i, err)
		}
		np, err := NormalizePath(string(raw))
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if _, dup := seen[np]; dup {
			return nil, fmt.Errorf("entry %d: duplicate path %q", i, np)
		}
		seen[np] = struct{}{}

		var size uint64
		if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
			return nil, fmt.Errorf("entry %d: reading size: %w", i, err)
		}
		if size > MaxFileSize {
			return nil, fmt.Errorf("entry %d: size %d exceeds cap %d", i, size, MaxFileSize)
		}
		total += size
		if total > MaxTotal {
			return nil, fmt.Errorf("entry %d: aggregate size exceeds cap %d", i, MaxTotal)
		}

		var digest [sha256.Size]byte
		if _, err := io.ReadFull(r, digest[:]); err != nil {
			return nil, fmt.Errorf("entry %d: reading digest: %w", i, err)
		}

		// Size validated above; allocation is bounded by MaxFileSize.
		content := make([]byte, size)
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, fmt.Errorf("entry %d: reading content: %w", i, err)
		}
		if sum := sha256.Sum256(content); sum != digest {
			return nil, fmt.Errorf("entry %d %q: digest mismatch", i, np)
		}

		entries = append(entries, Entry{
			Path:    np,
			Size:    size,
			Digest:  digest,
			Content: content,
		})
	}
	return entries, nil
}

// PackBytes is a convenience helper: build an archive from name->content.
func PackBytes(files map[string][]byte) ([]byte, error) {
	entries := make([]Entry, 0, len(files))
	for name, content := range files {
		entries = append(entries, Entry{
			Path:    name,
			Size:    uint64(len(content)),
			Digest:  sha256.Sum256(content),
			Content: content,
		})
	}
	var buf bytes.Buffer
	if err := Encode(&buf, entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
