package netsec

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
)

const tempAttempts = 16

// WriteFile atomically replaces name with data. The new content is written to
// an exclusively created temporary file in the same directory, flushed, and
// renamed over the destination. The destination inode after the rename must
// be the inode that was written.
func (r *Root) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return r.WriteFunc(name, perm, func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	})
}

// AtomicWrite is an alias for WriteFile.
func (r *Root) AtomicWrite(name string, data []byte, perm fs.FileMode) error {
	return r.WriteFile(name, data, perm)
}

// WriteFrom atomically replaces name with the contents of src.
func (r *Root) WriteFrom(name string, src io.Reader, perm fs.FileMode) error {
	return r.WriteFunc(name, perm, func(w io.Writer) error {
		_, err := io.Copy(w, src)
		return err
	})
}

// WriteFunc atomically replaces name with data produced by fn. fn must not
// close the supplied writer; it may only write to it.
func (r *Root) WriteFunc(name string, perm fs.FileMode, fn func(io.Writer) error) error {
	if fn == nil {
		return fmt.Errorf("%w: nil writer function", ErrInvalidPath)
	}
	rel, err := normalizeRequest(name)
	if err != nil {
		return err
	}
	if requestOrRoot(rel) {
		return fmt.Errorf("%w: cannot write the module root", ErrInvalidPath)
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := r.relativeResolved(rel); err != nil {
		return err
	}

	parentRel := path.Dir(rel)
	parent, err := r.openDir(handle, parentRel)
	if err != nil {
		return err
	}
	defer parent.close()
	base := path.Base(rel)

	mode := perm
	targetInfo, statErr := parent.root.Lstat(base)
	switch {
	case statErr == nil:
		if targetInfo.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: refusing to replace %q", ErrSymlink, rel)
		}
		if targetInfo.IsDir() {
			return fmt.Errorf("%w: %q is a directory", ErrInvalidPath, rel)
		}
		if mode == 0 {
			mode = targetInfo.Mode().Perm()
		}
	case errors.Is(statErr, fs.ErrNotExist):
		if mode == 0 {
			mode = 0o644
		}
	default:
		return statErr
	}
	mode &= fs.ModePerm

	tempBase, err := randomTempName(base)
	if err != nil {
		return err
	}

	var (
		file     *os.File
		openErr  error
		tempInfo fs.FileInfo
	)
	for attempt := 0; attempt < tempAttempts; attempt++ {
		file, openErr = parent.root.OpenFile(tempBase, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if openErr == nil {
			break
		}
		if !errors.Is(openErr, fs.ErrExist) {
			return openErr
		}
		tempBase, err = randomTempName(base)
		if err != nil {
			return err
		}
	}
	if openErr != nil {
		return fmt.Errorf("netsec: create temporary file: %w", openErr)
	}

	installed := false
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if !installed {
			_ = parent.root.Remove(tempBase)
		}
	}()

	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("netsec: chmod temporary file: %w", err)
	}
	if err := fn(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("netsec: sync temporary file: %w", err)
	}
	tempInfo, err = file.Stat()
	if err != nil {
		return fmt.Errorf("netsec: stat temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("netsec: close temporary file: %w", err)
	}
	closed = true

	onDisk, err := parent.root.Lstat(tempBase)
	if err != nil {
		return fmt.Errorf("netsec: re-stat temporary file: %w", err)
	}
	if !os.SameFile(tempInfo, onDisk) {
		return fmt.Errorf("%w: temporary file was replaced", ErrInodeMismatch)
	}
	if err := r.verifyDir(handle, parent); err != nil {
		return err
	}

	if err := renameInDir(parent, tempBase, parent, base); err != nil {
		return fmt.Errorf("netsec: install temporary file: %w", err)
	}
	installed = true
	_ = parent.file.Sync()

	installedInfo, err := parent.root.Lstat(base)
	if err != nil {
		return fmt.Errorf("%w: stat installed file: %v", ErrInodeMismatch, err)
	}
	if !os.SameFile(tempInfo, installedInfo) {
		return fmt.Errorf("%w: installed file is not the written file", ErrInodeMismatch)
	}
	if err := r.verifyDir(handle, parent); err != nil {
		return err
	}
	return nil
}

func randomTempName(base string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("netsec: generate temporary name: %w", err)
	}
	if len(base) > 96 {
		base = base[:96]
	}
	return fmt.Sprintf(".%s.tmp-%x", base, random), nil
}
