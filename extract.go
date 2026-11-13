package netsec

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrNestingTooDeep is returned when an archive nests further than the
	// caller-configured layer limit.
	ErrNestingTooDeep = errors.New("netsec: archive nesting exceeds caller limit")
	// ErrUnsafePath is returned when an entry path would escape the
	// extraction directory.
	ErrUnsafePath = errors.New("netsec: archive entry path is unsafe")
	// ErrUnsupportedEntry is returned for entry types that are neither
	// regular files nor directories (e.g. symlinks, devices).
	ErrUnsupportedEntry = errors.New("netsec: unsupported archive entry type")
)

// DefaultMaxDepth is the nesting limit used when Extractor.MaxDepth <= 0.
const DefaultMaxDepth = 4

// Extractor extracts tar archives, recursing into nested *.tar members.
// The zero value is ready to use with DefaultMaxDepth.
type Extractor struct {
	// MaxDepth is the maximum number of archive layers that may be opened.
	// The outermost archive is layer 1. Values <= 0 select DefaultMaxDepth.
	MaxDepth int
}

func (e Extractor) limit() int {
	if e.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return e.MaxDepth
}

// Extract reads a tar stream from r and extracts it under dir. Members
// whose names end in ".tar" are treated as nested archives and extracted
// in place (into a directory named after the member minus the suffix),
// subject to the layer limit. Every layer re-validates entry paths.
func (e Extractor) Extract(r io.Reader, dir string) error {
	return e.extract(r, dir, 1)
}

// Extract is a convenience wrapper around Extractor{MaxDepth: maxDepth}.
func Extract(r io.Reader, dir string, maxDepth int) error {
	return Extractor{MaxDepth: maxDepth}.Extract(r, dir)
}

func (e Extractor) extract(r io.Reader, dir string, depth int) error {
	if depth > e.limit() {
		return fmt.Errorf("%w: layer %d > limit %d", ErrNestingTooDeep, depth, e.limit())
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("netsec: read archive layer %d: %w", depth, err)
		}
		target, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("netsec: create directory: %w", err)
			}
		case tar.TypeReg:
			if isNestedArchive(hdr.Name) {
				nestedDir := strings.TrimSuffix(target, ".tar")
				if err := e.extract(tr, nestedDir, depth+1); err != nil {
					return err
				}
				continue
			}
			if err := writeFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: %q (typeflag %d)", ErrUnsupportedEntry, hdr.Name, hdr.Typeflag)
		}
	}
}

func isNestedArchive(name string) bool {
	return strings.HasSuffix(name, ".tar")
}

// safeJoin resolves an archive entry name against dir, rejecting absolute
// paths and any path that would escape dir.
func safeJoin(dir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty name", ErrUnsafePath)
	}
	clean := path.Clean(name)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	target := filepath.Join(dir, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	return target, nil
}

func writeFile(target string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("netsec: create parent directory: %w", err)
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("netsec: create file: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("netsec: write file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("netsec: close file: %w", err)
	}
	return nil
}
