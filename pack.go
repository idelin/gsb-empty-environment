package safearch

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Pack serializes the directory tree rooted at root into w. Entries are
// sorted by path for deterministic output. Only regular files and
// directories are accepted; anything else (symlinks included) fails the
// pack before a single byte of its payload is emitted.
func Pack(root string, w io.Writer) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: pack root %q is not a directory", ErrUnsupported, root)
	}

	type item struct {
		rel  string
		mode fs.FileMode
		size int64
		dir  bool
	}
	var items []item
	seen := newEntrySet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name, err := ValidatePath(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		m := fi.Mode()
		switch {
		case m.IsDir():
			if err := seen.add(name, true); err != nil {
				return err
			}
			items = append(items, item{name, m.Perm(), 0, true})
		case m.IsRegular():
			if m.Perm()&^0o777 != 0 {
				return fmt.Errorf("%w: %q has setuid/setgid/sticky bits", ErrUnsupported, name)
			}
			if err := seen.add(name, false); err != nil {
				return err
			}
			items = append(items, item{name, m.Perm(), fi.Size(), false})
		default:
			return fmt.Errorf("%w: %q (%s)", ErrUnsupported, name, m.Type())
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].rel < items[j].rel })

	overall := sha256.New()
	bw := bufio.NewWriter(io.MultiWriter(w, overall))
	var num [8]byte

	write := func(p []byte) error {
		_, err := bw.Write(p)
		return err
	}
	if err := write([]byte(Magic)); err != nil {
		return err
	}
	if err := bw.WriteByte(Version); err != nil {
		return err
	}
	for _, it := range items {
		if it.dir {
			if err := bw.WriteByte(typDir); err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(num[:2], uint16(len(it.rel)))
			if err := write(num[:2]); err != nil {
				return err
			}
			if err := write([]byte(it.rel)); err != nil {
				return err
			}
			binary.LittleEndian.PutUint32(num[:4], uint32(it.mode))
			if err := write(num[:4]); err != nil {
				return err
			}
			continue
		}
		if err := bw.WriteByte(typFile); err != nil {
			return err
		}
		binary.LittleEndian.PutUint16(num[:2], uint16(len(it.rel)))
		if err := write(num[:2]); err != nil {
			return err
		}
		if err := write([]byte(it.rel)); err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(num[:4], uint32(it.mode))
		if err := write(num[:4]); err != nil {
			return err
		}
		binary.LittleEndian.PutUint64(num[:8], uint64(it.size))
		if err := write(num[:8]); err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(it.rel)))
		if err != nil {
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(bw, h), io.LimitReader(f, it.size+1))
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != it.size {
			return fmt.Errorf("%w: %q changed size while packing", ErrFormat, it.rel)
		}
		// The per-entry digest follows the payload and must not be hashed
		// into the overall digest, so write it directly to w.
		if err := bw.Flush(); err != nil {
			return err
		}
		if _, err := w.Write(h.Sum(nil)); err != nil {
			return err
		}
	}
	if err := bw.WriteByte(typEnd); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if _, err := w.Write(overall.Sum(nil)); err != nil {
		return err
	}
	return nil
}

// PackFile packs root and atomically publishes the archive at dst. The
// archive is first written to a temporary file in the same directory and
// renamed into place; dst must not exist yet, and a failed pack leaves no
// partial output behind.
func PackFile(root, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %q", ErrExists, dst)
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".sar-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if err := Pack(root, tmp); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	ok = true
	return syncDir(filepath.Dir(dst))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
