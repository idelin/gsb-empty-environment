package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
)

// Rename moves oldName to newName within the same module root. Both the source
// and destination parents are checked for symbolic-link escape, and the
// destination inode is verified after the move.
func (r *Root) Rename(oldName, newName string) error {
	oldRel, err := normalizeRequest(oldName)
	if err != nil {
		return err
	}
	newRel, err := normalizeRequest(newName)
	if err != nil {
		return err
	}
	if requestOrRoot(oldRel) || requestOrRoot(newRel) {
		return fmt.Errorf("%w: cannot rename the module root", ErrInvalidPath)
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := r.relativeResolved(oldRel); err != nil {
		return err
	}
	if _, err := r.relativeResolved(newRel); err != nil {
		return err
	}

	if oldRel == newRel {
		_, err := handle.Lstat(oldRel)
		return err
	}

	oldParent, err := r.openDir(handle, path.Dir(oldRel))
	if err != nil {
		return err
	}
	defer oldParent.close()
	newParent, err := r.openDir(handle, path.Dir(newRel))
	if err != nil {
		return err
	}
	defer newParent.close()

	oldBase := path.Base(oldRel)
	newBase := path.Base(newRel)
	sourceInfo, err := oldParent.root.Lstat(oldBase)
	if err != nil {
		return err
	}

	if err := renameInDir(oldParent, oldBase, newParent, newBase); err != nil {
		return fmt.Errorf("netsec: rename: %w", err)
	}

	if err := r.verifyDir(handle, oldParent); err != nil {
		return err
	}
	if err := r.verifyDir(handle, newParent); err != nil {
		return err
	}

	movedInfo, err := newParent.root.Lstat(newBase)
	if err != nil {
		return fmt.Errorf("%w: stat renamed object: %v", ErrInodeMismatch, err)
	}
	if !os.SameFile(sourceInfo, movedInfo) {
		return fmt.Errorf("%w: renamed object is not the source object", ErrInodeMismatch)
	}
	if remaining, err := oldParent.root.Lstat(oldBase); err == nil && os.SameFile(sourceInfo, remaining) {
		return fmt.Errorf("%w: source still exists after rename", ErrInodeMismatch)
	}
	return nil
}

// Remove deletes a file, symbolic link, or empty directory inside the root.
// A target whose resolved path leaves the root is rejected. The parent inode
// is checked before and after, and the removed inode must no longer be present
// at the request path.
func (r *Root) Remove(name string) error {
	rel, err := normalizeRequest(name)
	if err != nil {
		return err
	}
	if requestOrRoot(rel) {
		return fmt.Errorf("%w: cannot remove the module root", ErrInvalidPath)
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := r.relativeResolved(rel); err != nil {
		return err
	}
	parent, err := r.openDir(handle, path.Dir(rel))
	if err != nil {
		return err
	}
	defer parent.close()
	base := path.Base(rel)
	before, err := parent.root.Lstat(base)
	if err != nil {
		return err
	}

	if err := parent.root.Remove(base); err != nil {
		return err
	}
	if err := r.verifyDir(handle, parent); err != nil {
		return err
	}

	after, err := parent.root.Lstat(base)
	if err == nil && os.SameFile(before, after) {
		return fmt.Errorf("%w: removed object is still present", ErrInodeMismatch)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: verify removal: %v", ErrInodeMismatch, err)
	}
	return nil
}

// RemoveAll deletes a file, symbolic link, or directory tree inside the
// module root. It never follows symbolic links to directories. A symbolic
// link whose target leaves the root is rejected rather than unlinked, and
// every directory is pinned and inode-checked while its children are
// processed.
func (r *Root) RemoveAll(name string) error {
	rel, err := normalizeRequest(name)
	if err != nil {
		return err
	}
	if requestOrRoot(rel) {
		return fmt.Errorf("%w: cannot remove the module root", ErrInvalidPath)
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := r.relativeResolved(rel); err != nil {
		return err
	}
	return r.removeAllAt(handle, rel)
}

func (r *Root) removeAllAt(handle *os.Root, rel string) error {
	parent, err := r.openDir(handle, path.Dir(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.close()

	base := path.Base(rel)
	info, err := parent.root.Lstat(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		if info.Mode()&fs.ModeSymlink != 0 {
			if _, err := r.relativeResolved(rel); err != nil {
				return err
			}
		}
		return r.removeOne(parent, base, info)
	}

	dirRoot, err := parent.root.OpenRoot(base)
	if err != nil {
		return err
	}
	dirFile, err := dirRoot.Open(".")
	if err != nil {
		_ = dirRoot.Close()
		return err
	}
	dirInfo, err := dirFile.Stat()
	if err != nil {
		_ = dirFile.Close()
		_ = dirRoot.Close()
		return err
	}
	if !os.SameFile(info, dirInfo) {
		_ = dirFile.Close()
		_ = dirRoot.Close()
		return fmt.Errorf("%w: directory %q changed while opening", ErrInodeMismatch, rel)
	}
	names, err := dirFile.Readdirnames(-1)
	closeErr := dirFile.Close()
	rootCloseErr := dirRoot.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if rootCloseErr != nil {
		return rootCloseErr
	}

	for _, child := range names {
		if err := r.removeAllAt(handle, path.Join(rel, child)); err != nil {
			return err
		}
	}
	now, err := parent.root.Lstat(base)
	if err != nil {
		return fmt.Errorf("%w: re-stat directory %q: %v", ErrInodeMismatch, rel, err)
	}
	if !os.SameFile(info, now) {
		return fmt.Errorf("%w: directory %q was replaced", ErrInodeMismatch, rel)
	}
	return r.removeOne(parent, base, now)
}

func (r *Root) removeOne(parent *dirHandle, base string, before fs.FileInfo) error {
	if err := parent.root.Remove(base); err != nil {
		return err
	}
	if err := r.verifyDir(nil, parent); err != nil {
		return err
	}
	after, err := parent.root.Lstat(base)
	if err == nil && os.SameFile(before, after) {
		return fmt.Errorf("%w: removed object is still present", ErrInodeMismatch)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: verify removal: %v", ErrInodeMismatch, err)
	}
	return nil
}
