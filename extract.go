package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Extract unpacks verified entries into dest. The destination is never
// modified in place:
//
//  1. A fresh staging directory is created next to dest.
//  2. Every file is validated against path traversal and symlink escape,
//     then written to the staging tree.
//  3. Only after all files succeed, staging is atomically renamed to dest.
//
// dest must not already exist. On any failure the staging directory is
// removed and dest is left exactly as it was (absent): no partial output.
func Extract(entries []Entry, dest string) (err error) {
	if len(entries) > MaxEntries {
		return fmt.Errorf("too many entries: %d", len(entries))
	}
	parent := filepath.Dir(dest)
	if err := ensureRealDir(parent); err != nil {
		return fmt.Errorf("destination parent unusable: %w", err)
	}

	stage, err := os.MkdirTemp(parent, ".sar-stage-*")
	if err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()

	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		target, err := secureJoin(stage, e.Path)
		if err != nil {
			return fmt.Errorf("entry %q: %w", e.Path, err)
		}
		// Portable duplicate check (archive paths use '/' separators).
		key := filepath.FromSlash(e.Path)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate path: %q", e.Path)
		}
		seen[key] = struct{}{}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("entry %q: creating parent dirs: %w", e.Path, err)
		}
		// O_EXCL guarantees the file did not exist: earlier entries can never
		// be overwritten, and no symlink can already sit at this path inside
		// the freshly created tree.
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("entry %q: creating file: %w", e.Path, err)
		}
		if _, werr := f.Write(e.Content); werr != nil {
			_ = f.Close()
			return fmt.Errorf("entry %q: writing content: %w", e.Path, werr)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("entry %q: closing file: %w", e.Path, err)
		}
	}

	// Publish: refuse to overwrite an existing destination (including a
	// symlink), then atomically move staging into place.
	info, err := os.Lstat(dest)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to publish: destination is a symlink: %q", dest)
		}
		return fmt.Errorf("destination already exists: %q", dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking destination: %w", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return fmt.Errorf("publishing output: %w", err)
	}
	committed = true
	return nil
}

// ensureRealDir verifies that parent exists, is a directory, and is not a
// symlink, so MkdirTemp cannot be redirected elsewhere.
func ensureRealDir(parent string) error {
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("parent path is a symlink: %q", parent)
	}
	if !info.IsDir() {
		return fmt.Errorf("parent path is not a directory: %q", parent)
	}
	return nil
}

// secureJoin maps an already-normalized archive path onto base, rejecting
// any absolute path, ".." component, or symlink component that could make
// the final target escape base. The returned path is guaranteed to be
// inside base.
func secureJoin(base, name string) (string, error) {
	np, err := NormalizePath(name)
	if err != nil {
		return "", err
	}
	cleanBase := filepath.Clean(base)
	rel := filepath.FromSlash(np)
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path rejected: %q", name)
	}

	// Walk each component and Lstat it once it exists: any symlink in the
	// chain is a potential escape vector and is rejected.
	cur := cleanBase
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("parent directory component rejected in %q", name)
		}
		info, lerr := os.Lstat(cur)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			// Component does not exist yet; it will be created as a real
			// directory inside the staging tree, so it cannot be a symlink.
		case lerr != nil:
			return "", lerr
		case info.Mode()&os.ModeSymlink != 0:
			return "", fmt.Errorf("symlink component rejected: %q", cur)
		}
		cur = filepath.Join(cur, part)
	}

	target := filepath.Join(cleanBase, rel)
	// Belt-and-suspenders containment check after joining.
	r, err := filepath.Rel(cleanBase, target)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) || filepath.IsAbs(r) {
		return "", fmt.Errorf("path escapes staging root: %q", name)
	}
	return target, nil
}
