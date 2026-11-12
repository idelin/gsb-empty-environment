package netsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Checkout validates the complete tree entries and submodule records and,
// only if everything is safe, writes them under root. Validation happens
// entirely before any filesystem mutation, so a rejected checkout creates
// no files.
func Checkout(root string, entries []Entry, subs []Submodule) error {
	if err := ValidateEntries(entries); err != nil {
		return err
	}
	for _, sub := range subs {
		if err := ValidateSubmodule(sub, entries); err != nil {
			return err
		}
	}
	// Pre-scan the target tree for symlinked ancestors already on disk so
	// that a rejection still happens before a single file is written.
	for _, e := range entries {
		if err := checkDiskAncestors(root, e.Path); err != nil {
			return err
		}
	}
	for _, sub := range subs {
		if err := checkDiskAncestors(root, sub.Path); err != nil {
			return err
		}
	}

	for _, e := range entries {
		dest := filepath.Join(root, filepath.FromSlash(e.Path))
		switch e.Type {
		case EntryDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case EntryFile:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dest, e.Data, 0o644); err != nil {
				return err
			}
		case EntrySymlink:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(e.Target, dest); err != nil {
				return err
			}
		}
	}
	for _, sub := range subs {
		dest := filepath.Join(root, filepath.FromSlash(sub.Path))
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// checkDiskAncestors ensures that root and every ancestor of p under root
// is not a symlink on disk.
func checkDiskAncestors(root, p string) error {
	info, err := os.Lstat(root)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("checkout root %q is a symlink", root)
	}
	segs := strings.Split(p, "/")
	cur := root
	for _, seg := range segs[:len(segs)-1] {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q has symlinked parent on disk: %q", p, cur)
		}
	}
	return nil
}
