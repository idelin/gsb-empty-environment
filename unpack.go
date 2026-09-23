package safearch

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Unpack extracts the archive in r into dest. Extraction is staged: files
// are written to a private temporary directory, fully verified (sizes,
// per-entry digests, overall digest), and only then published into dest.
// dest must not exist yet; on any failure no partial output remains.
func Unpack(r io.Reader, dest string, lim Limits) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%w: %q", ErrExists, dest)
	} else if !os.IsNotExist(err) {
		return err
	}

	stage, err := os.MkdirTemp("", "sar-unpack-*")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, 0o700); err != nil {
		return err
	}

	err = scan(r, lim, func(e Entry, body io.Reader) (int64, []byte, error) {
		target, err := safeJoin(stage, e.Name)
		if err != nil {
			return 0, nil, err
		}
		if e.Dir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return 0, nil, err
			}
			if e.Mode != 0 && e.Mode != 0o755 {
				if err := os.Chmod(target, e.Mode); err != nil {
					return 0, nil, err
				}
			}
			return 0, nil, nil
		}
		if err := ensureParents(target); err != nil {
			return 0, nil, err
		}
		// O_EXCL: a duplicate path can never overwrite an earlier entry.
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return 0, nil, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, h), body)
		syncErr := f.Sync()
		closeErr := f.Close()
		if copyErr != nil {
			return n, nil, copyErr
		}
		if syncErr != nil {
			return n, nil, syncErr
		}
		if closeErr != nil {
			return n, nil, closeErr
		}
		if err := os.Chmod(target, e.Mode); err != nil {
			return n, nil, err
		}
		return n, h.Sum(nil), nil
	})
	if err != nil {
		return err
	}

	// Publish: same-filesystem rename, so readers either see nothing or the
	// complete, verified tree.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(stage, dest); err != nil {
		return err
	}
	ok = true
	return syncDir(filepath.Dir(dest))
}

// safeJoin resolves name inside root and proves the result stays there:
// lexically (prefix check) and physically (no symlink components).
func safeJoin(root, name string) (string, error) {
	clean, err := ValidatePath(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q escapes extraction root", ErrUnsafePath, name)
	}
	// Walk every existing component: none may be a symlink.
	cur := root
	for _, comp := range strings.Split(filepath.FromSlash(clean), string(filepath.Separator)) {
		cur = filepath.Join(cur, comp)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symlink component in %q", ErrUnsafePath, name)
		}
	}
	return target, nil
}

func ensureParents(target string) error {
	return os.MkdirAll(filepath.Dir(target), 0o755)
}
